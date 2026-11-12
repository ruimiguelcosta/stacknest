import Foundation

struct CLIResult {
    let status: Int32
    let stdout: Data
    let stderr: String

    var ok: Bool { status == 0 }
    var output: String { String(decoding: stdout, as: UTF8.self) }
    /// The message to show when the command failed.
    var message: String {
        let text = stderr.trimmingCharacters(in: .whitespacesAndNewlines)
        let cleaned = text.replacingOccurrences(of: "stacknest: ", with: "")
        return cleaned.isEmpty ? output.trimmingCharacters(in: .whitespacesAndNewlines) : cleaned
    }
}

/// Runs the stacknest command-line program, which does the actual work.
enum CLI {
    static let home = URL(fileURLWithPath: NSHomeDirectory()).appendingPathComponent(".stacknest")
    static let installed = home.appendingPathComponent("bin/stacknest")
    /// The copy shipped inside the app, used to install or update the service.
    static let bundled = Bundle.main.bundleURL.appendingPathComponent("Contents/Helpers/stacknest")
    static let statusFile = home.appendingPathComponent("run/status.json")

    static var executable: URL {
        FileManager.default.isExecutableFile(atPath: installed.path) ? installed : bundled
    }

    static var isInstalled: Bool { FileManager.default.isExecutableFile(atPath: installed.path) }

    /// True when the app ships a different build than the one installed.
    static var updateAvailable: Bool {
        let fm = FileManager.default
        guard fm.isExecutableFile(atPath: bundled.path), fm.isExecutableFile(atPath: installed.path) else { return false }
        return !fm.contentsEqual(atPath: bundled.path, andPath: installed.path)
    }

    static var environment: [String: String] {
        var env = ProcessInfo.processInfo.environment
        let extra = [home.appendingPathComponent("bin").path, "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"]
        env["PATH"] = (extra + [env["PATH"] ?? ""]).joined(separator: ":")
        return env
    }

    static func run(_ arguments: [String], using program: URL? = nil, in directory: URL? = nil) async -> CLIResult {
        await Task.detached { runBlocking(arguments, using: program, in: directory) }.value
    }

    private static func runBlocking(_ arguments: [String], using program: URL?, in directory: URL?) -> CLIResult {
        let process = Process()
        process.executableURL = program ?? executable
        process.arguments = arguments
        process.environment = environment
        if let directory { process.currentDirectoryURL = directory }
        let out = Pipe(), err = Pipe()
        process.standardOutput = out
        process.standardError = err
        do {
            try process.run()
        } catch {
            return CLIResult(status: -1, stdout: Data(), stderr: error.localizedDescription)
        }
        // Read both pipes before waiting, so a full pipe cannot block the child.
        var stderrData = Data()
        let group = DispatchGroup()
        group.enter()
        DispatchQueue.global().async {
            stderrData = err.fileHandleForReading.readDataToEndOfFile()
            group.leave()
        }
        let stdoutData = out.fileHandleForReading.readDataToEndOfFile()
        group.wait()
        process.waitUntilExit()
        return CLIResult(status: process.terminationStatus, stdout: stdoutData, stderr: String(decoding: stderrData, as: UTF8.self))
    }

    /// Runs a long command and reports its output line by line.
    static func stream(_ arguments: [String], in directory: URL? = nil, onOutput: @escaping @Sendable (String) -> Void) async -> Int32 {
        await Task.detached {
            let process = Process()
            process.executableURL = executable
            process.arguments = arguments
            process.environment = environment
            if let directory { process.currentDirectoryURL = directory }
            let pipe = Pipe()
            process.standardOutput = pipe
            process.standardError = pipe
            pipe.fileHandleForReading.readabilityHandler = { handle in
                let data = handle.availableData
                if !data.isEmpty { onOutput(String(decoding: data, as: UTF8.self)) }
            }
            do {
                try process.run()
            } catch {
                onOutput(error.localizedDescription + "\n")
                return -1
            }
            process.waitUntilExit()
            pipe.fileHandleForReading.readabilityHandler = nil
            let rest = pipe.fileHandleForReading.readDataToEndOfFile()
            if !rest.isEmpty { onOutput(String(decoding: rest, as: UTF8.self)) }
            return process.terminationStatus
        }.value
    }

    static func decode<T: Decodable>(_ type: T.Type, from result: CLIResult) throws -> T {
        guard result.ok else { throw CLIError(message: result.message) }
        return try JSONDecoder.stacknest.decode(type, from: result.stdout)
    }
}

struct CLIError: LocalizedError {
    let message: String
    var errorDescription: String? { message }
}

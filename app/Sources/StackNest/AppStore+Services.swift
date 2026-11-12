import Foundation

extension AppStore {
    /// True until the engine and at least one PHP version are installed.
    var needsOnboarding: Bool {
        !CLI.isInstalled || !installed.contains { $0.name == "php" }
    }

    var databaseInstances: [ServiceInstance] { settings?.databases ?? [] }

    func loadRuntimes() async {
        async let catalogResult = CLI.run(["runtime", "catalog"])
        async let installedResult = CLI.run(["runtime", "list"])
        async let settingsResult = CLI.run(["service", "list"])
        do {
            catalog = try CLI.decode(RuntimeCatalog.self, from: await catalogResult).runtimes
            catalogError = nil
        } catch {
            catalogError = error.localizedDescription
        }
        installed = (try? CLI.decode([InstalledRuntime].self, from: await installedResult)) ?? []
        settings = try? CLI.decode(ServiceSettings.self, from: await settingsResult)
    }

    func catalogEntries(_ name: String) -> [CatalogEntry] {
        catalog.filter { $0.name == name }
    }

    func installedRuntime(_ name: String, _ series: String) -> InstalledRuntime? {
        installed.first { $0.name == name && $0.series == series }
    }

    /// Runs a command that may download a runtime, following its progress lines.
    @discardableResult
    func runWithProgress(_ arguments: [String], key: String) async -> String? {
        progress[key] = 0
        let log = OutputBox()
        let status = await CLI.stream(arguments) { text in
            log.append(text)
            let percents = text.split(separator: "\n").compactMap { line in
                line.hasPrefix("progress ") ? Double(line.dropFirst("progress ".count)) : nil
            }
            if let percent = percents.last {
                Task { @MainActor in
                    if self.progress[key] != nil { self.progress[key] = percent / 100 }
                }
            }
        }
        progress[key] = nil
        await loadRuntimes()
        await loadPHPVersions()
        if status == 0 { return nil }
        // The last line names the error.
        let lines = log.text.split(separator: "\n").map(String.init).filter { !$0.hasPrefix("progress ") }
        return (lines.last ?? "Falhou.").replacingOccurrences(of: "stacknest: ", with: "")
    }

    func installRuntime(_ name: String, _ series: String) async {
        if let error = await runWithProgress(["runtime", "install", name, series], key: name + "-" + series) {
            alert = error
        }
    }

    func removeRuntime(_ name: String, _ series: String) async {
        let result = await CLI.run(["runtime", "remove", name, series])
        if !result.ok { alert = result.message }
        await loadRuntimes()
        await loadPHPVersions()
    }

    func enableService(_ engine: String, version: String = "", port: Int? = nil) async -> String? {
        var arguments = ["service", "enable", engine]
        if !version.isEmpty { arguments += ["--version", version] }
        if let port { arguments += ["--port", String(port)] }
        return await runWithProgress(arguments, key: engine + "-" + version)
    }

    func disableService(_ engine: String, version: String = "") async {
        var arguments = ["service", "disable", engine]
        if !version.isEmpty { arguments += ["--version", version] }
        let result = await CLI.run(arguments)
        if !result.ok { alert = result.message }
        await loadRuntimes()
    }

    // MARK: - Project services

    func createDatabase(for project: ProjectState, instance: ServiceInstance, name: String, writeEnv: Bool) async -> (ok: Bool, message: String) {
        busy = true
        defer { busy = false }
        var arguments = ["db", "create", "--engine", instance.engine, "--version", instance.version]
        if !name.isEmpty { arguments += ["--name", name] }
        if writeEnv { arguments.append("--env") }
        let result = await CLI.run(arguments + [project.dir])
        return (result.ok, result.ok ? result.output.trimmingCharacters(in: .whitespacesAndNewlines) : result.message)
    }

    func applyEnv(to project: ProjectState, mail: Bool, cache: Bool, search: Bool) async -> (ok: Bool, message: String) {
        var arguments = ["env"]
        if mail { arguments.append("--mail") }
        if cache { arguments.append("--cache") }
        if search { arguments.append("--search") }
        let result = await CLI.run(arguments + [project.dir])
        let output = result.output.trimmingCharacters(in: .whitespacesAndNewlines)
        return (result.ok, result.ok ? (output.isEmpty ? ".env já estava atualizado." : output) : result.message)
    }

    func receiveMail(url: String, format: String, from: String, to: String, subject: String, text: String) async -> (ok: Bool, message: String) {
        let result = await CLI.run(["mail", "receive", "--url", url, "--format", format, "--from", from, "--to", to, "--subject", subject, "--text", text])
        return (result.ok, result.ok ? result.output : result.message)
    }

    func xdebugAvailable(php: String) -> Bool {
        FileManager.default.fileExists(atPath: CLI.home.appendingPathComponent("runtimes/php/\(php)/lib/xdebug.so").path)
    }

    // MARK: - App updates

    static let repository = "ruimiguelcosta/stacknest"

    func checkForAppUpdate() async {
        guard let current = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String,
              let url = URL(string: "https://api.github.com/repos/\(Self.repository)/releases/latest") else { return }
        var request = URLRequest(url: url)
        request.setValue("StackNest", forHTTPHeaderField: "User-Agent")
        guard let (data, response) = try? await URLSession.shared.data(for: request),
              (response as? HTTPURLResponse)?.statusCode == 200,
              let release = try? JSONDecoder().decode(GitHubRelease.self, from: data) else { return }
        let latest = release.tag_name.trimmingCharacters(in: CharacterSet(charactersIn: "v"))
        if latest.compare(current, options: .numeric) == .orderedDescending, let page = URL(string: release.html_url) {
            appUpdate = (latest, page)
        }
    }
}

/// Collects a command's output from the callbacks of CLI.stream.
final class OutputBox: @unchecked Sendable {
    private let lock = NSLock()
    private var buffer = ""

    func append(_ text: String) {
        lock.lock()
        buffer += text
        lock.unlock()
    }

    var text: String {
        lock.lock()
        defer { lock.unlock() }
        return buffer
    }
}

private struct GitHubRelease: Decodable {
    let tag_name: String
    let html_url: String
}

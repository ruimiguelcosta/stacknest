import AppKit
import Foundation
import Observation

@MainActor
@Observable
final class AppStore {
    var status: DaemonStatus?
    var daemonAlive = false
    var phpVersions: [PHPVersion] = []
    var laravelReleases: [LaravelRelease] = []
    var laravelError: String?
    var cveReport: CVEReport?
    var cveLoading = false
    var cveError: String?
    var resolverConfigured = false
    var caTrusted = false
    var engineUpdateAvailable = false
    var busy = false
    var alert: String?

    private var timer: Timer?

    var projects: [ProjectState] { status?.projects ?? [] }
    var services: [ServiceState] { status?.services ?? [] }
    var systemReady: Bool { resolverConfigured && caTrusted }

    func start() {
        refreshStatus()
        timer = Timer.scheduledTimer(withTimeInterval: 2, repeats: true) { [weak self] _ in
            Task { @MainActor in self?.refreshStatus() }
        }
        engineUpdateAvailable = CLI.updateAvailable
        Task {
            await loadPHPVersions()
            await checkSystem()
            await loadCVEs(refresh: false)
        }
    }

    func refreshStatus() {
        guard let data = try? Data(contentsOf: CLI.statusFile),
              let decoded = try? JSONDecoder.stacknest.decode(DaemonStatus.self, from: data) else {
            status = nil
            daemonAlive = false
            return
        }
        status = decoded
        // The daemon rewrites the file every two seconds.
        daemonAlive = Date().timeIntervalSince(decoded.updated) < 10
    }

    func loadPHPVersions() async {
        let result = await CLI.run(["php", "list", "--json"])
        phpVersions = (try? CLI.decode([PHPVersion].self, from: result)) ?? []
    }

    func loadLaravelReleases() async {
        guard laravelReleases.isEmpty else { return }
        do {
            laravelReleases = try CLI.decode([LaravelRelease].self, from: await CLI.run(["laravel", "versions", "--json"]))
            laravelError = nil
        } catch {
            laravelError = error.localizedDescription
        }
    }

    func loadCVEs(refresh: Bool) async {
        guard !cveLoading else { return }
        cveLoading = true
        defer { cveLoading = false }
        let result = await CLI.run(refresh ? ["cves", "--refresh"] : ["cves"])
        do {
            cveReport = try CLI.decode(CVEReport.self, from: result)
            cveError = nil
        } catch {
            cveError = error.localizedDescription
        }
    }

    // MARK: - Projects

    /// Saves settings; returns an error message, or nil on success.
    func save(_ project: ProjectState, arguments: [String]) async -> String? {
        guard !arguments.isEmpty else { return nil }
        busy = true
        defer { busy = false }
        let result = await CLI.run(["set"] + arguments + [project.dir])
        if result.ok {
            await loadCVEs(refresh: false)
        }
        return result.ok ? nil : result.message
    }

    func link(directory: URL, php: String, domain: String) async -> String? {
        busy = true
        defer { busy = false }
        var arguments = ["link", "--php", php]
        if !domain.isEmpty { arguments += ["--domain", domain] }
        let result = await CLI.run(arguments + [directory.path])
        return result.ok ? nil : result.message
    }

    func unlink(_ project: ProjectState) async {
        let result = await CLI.run(["unlink", project.dir])
        if !result.ok { alert = result.message }
    }

    // MARK: - Service

    func restart() async {
        busy = true
        defer { busy = false }
        let result = await CLI.run(["restart"])
        if !result.ok { alert = result.message }
    }

    /// Installs or updates the background service from the copy inside the app.
    func installEngine() async {
        busy = true
        defer { busy = false }
        let result = await CLI.run(["install"], using: CLI.bundled)
        if !result.ok {
            alert = result.message
        }
        engineUpdateAvailable = CLI.updateAvailable
        await loadPHPVersions()
    }

    // MARK: - System integration

    static let resolverFile = "/etc/resolver/test"
    static let resolverContents = "nameserver 127.0.0.1\nport 5354\n"
    static var rootCA: String { CLI.home.appendingPathComponent("caddy/pki/authorities/local/root.crt").path }

    func checkSystem() async {
        let contents = (try? String(contentsOfFile: Self.resolverFile, encoding: .utf8)) ?? ""
        resolverConfigured = contents.contains("port 5354")
        let verify = await CLI.run(["verify-cert", "-c", Self.rootCA, "-p", "ssl"], using: URL(fileURLWithPath: "/usr/bin/security"))
        caTrusted = verify.ok
    }

    /// Points .test at StackNest and trusts its certificate authority; macOS asks for the password.
    func configureSystem() async {
        let ca = Self.rootCA.replacingOccurrences(of: "'", with: "'\\''")
        let shell = """
        mkdir -p /etc/resolver && printf 'nameserver 127.0.0.1\\nport 5354\\n' > \(Self.resolverFile) && \
        security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain '\(ca)'
        """
        let escaped = shell.replacingOccurrences(of: "\\", with: "\\\\").replacingOccurrences(of: "\"", with: "\\\"")
        let source = "do shell script \"\(escaped)\" with administrator privileges"
        var errorInfo: NSDictionary?
        NSAppleScript(source: source)?.executeAndReturnError(&errorInfo)
        if let errorInfo, (errorInfo[NSAppleScript.errorNumber] as? Int) != -128 {
            alert = (errorInfo[NSAppleScript.errorMessage] as? String) ?? "Não foi possível configurar o sistema."
        }
        await checkSystem()
    }

    // MARK: - Security

    var latestFindings: [AggregatedFinding] {
        guard let report = cveReport else { return [] }
        var byKey: [String: AggregatedFinding] = [:]
        for project in report.projects ?? [] {
            for finding in project.findings {
                let key = finding.id + "|" + finding.component + "|" + finding.version
                byKey[key, default: AggregatedFinding(finding: finding, projects: [])].projects.append(project.domain)
            }
        }
        for runtime in report.runtimes ?? [] {
            for finding in runtime.findings {
                let key = finding.id + "|" + finding.component + "|" + finding.version
                let names = (runtime.projects ?? []).map { name in projects.first { $0.name == name }?.displayName ?? name }
                byKey[key, default: AggregatedFinding(finding: finding, projects: [])].projects.append(contentsOf: names)
            }
        }
        return byKey.values.sorted { $0.finding.published > $1.finding.published }
    }

    func findings(for project: ProjectState) -> [Finding] {
        guard let report = cveReport else { return [] }
        var list = report.projects?.first { $0.name == project.name }?.findings ?? []
        for runtime in report.runtimes ?? [] where runtime.projects?.contains(project.name) == true {
            list += runtime.findings
        }
        return list.sorted { $0.severity == $1.severity ? $0.published > $1.published : $0.severity < $1.severity }
    }

    func severityCounts(_ findings: [Finding]) -> [Severity: Int] {
        Dictionary(grouping: findings, by: \.severity).mapValues(\.count)
    }
}

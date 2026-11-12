import Foundation

// Mirrors the JSON written by the stacknest daemon and CLI.

struct OPcache: Codable, Equatable {
    var enabled: Bool
    var memory: Int
    var validate_timestamps: Bool
    var jit: Bool
}

struct ServiceState: Codable, Identifiable {
    var id: String { name }
    let name: String
    let title: String?
    let running: Bool
    let port: Int?
    let web_url: String?
    let log: String?
    let error: String?

    var displayTitle: String {
        if name == "mailpit" { return "Mailpit (email local)" }
        return title ?? name
    }
}

struct ProjectState: Codable, Identifiable, Hashable {
    var id: String { name }
    let name: String
    let dir: String
    let domain: String?
    let aliases: [String]?
    let php: String?
    let root: String?
    let `static`: Bool?
    let custom: Bool?
    let running: Bool
    let opcache: OPcache
    let xdebug: Bool?
    let database: ProjectDatabase?
    let log: String?
    let error: String?

    var url: URL? { domain.flatMap { URL(string: "https://\($0)") } }
    var displayName: String { domain ?? name }

    static func == (a: ProjectState, b: ProjectState) -> Bool { a.name == b.name }
    func hash(into hasher: inout Hasher) { hasher.combine(name) }
}

struct ProjectDatabase: Codable, Hashable {
    let engine: String
    let version: String
}

struct DaemonStatus: Codable {
    let updated: Date
    let pid: Int
    let https_port: Int
    let services: [ServiceState]?
    let projects: [ProjectState]?
    let web_error: String?
}

struct PHPVersion: Codable, Identifiable, Hashable {
    var id: String { version }
    let version: String
    let full: String
}

struct LaravelRelease: Codable, Identifiable, Hashable {
    var id: Int { major }
    let major: Int
    let version: String
    let php_constraint: String
    let default_php: String
    let compatible_php: [String]?
}

enum Severity: String, Codable, CaseIterable, Comparable {
    case CRITICAL, HIGH, MEDIUM, LOW, UNKNOWN

    var label: String {
        switch self {
        case .CRITICAL: return "Crítica"
        case .HIGH: return "Alta"
        case .MEDIUM: return "Média"
        case .LOW: return "Baixa"
        case .UNKNOWN: return "Sem classificação"
        }
    }

    private var rank: Int { Severity.allCases.firstIndex(of: self)! }
    static func < (a: Severity, b: Severity) -> Bool { a.rank < b.rank }

    init(from decoder: Decoder) throws {
        let raw = try decoder.singleValueContainer().decode(String.self)
        self = Severity(rawValue: raw) ?? .UNKNOWN
    }
}

struct Finding: Codable, Hashable {
    let id: String
    let aliases: [String]?
    let component: String
    let version: String
    let severity: Severity
    let score: Double?
    let summary: String
    let published: Date
    let fixed: String?
    let url: String
    let source: String
}

struct ProjectReport: Codable, Identifiable {
    var id: String { name }
    let name: String
    let domain: String
    let dir: String
    let packages: Int
    let findings: [Finding]
    let error: String?
}

struct RuntimeReport: Codable, Identifiable {
    var id: String { component + version }
    let component: String
    let version: String
    let projects: [String]?
    let findings: [Finding]
    let error: String?
}

struct CVEReport: Codable {
    let generated: Date
    let projects: [ProjectReport]?
    let runtimes: [RuntimeReport]?
}

/// One advisory with every project it affects, for the home page.
struct AggregatedFinding: Identifiable {
    var id: String { finding.id + "|" + finding.component + "|" + finding.version }
    let finding: Finding
    var projects: [String]
}

extension JSONDecoder {
    /// Go writes RFC 3339 times, with or without fractional seconds.
    static let stacknest: JSONDecoder = {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .custom { decoder in
            let text = try decoder.singleValueContainer().decode(String.self)
            let fractional = ISO8601DateFormatter()
            fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
            if let date = fractional.date(from: text) { return date }
            let plain = ISO8601DateFormatter()
            if let date = plain.date(from: text) { return date }
            return .distantPast
        }
        return decoder
    }()
}

// MARK: - Runtimes and services

struct CatalogEntry: Codable, Identifiable, Hashable {
    var id: String { name + "-" + series }
    let name: String
    let series: String
    let version: String
    let size: Int64?
}

struct RuntimeCatalog: Codable {
    let generated: Date?
    let runtimes: [CatalogEntry]
}

struct InstalledRuntime: Codable, Identifiable, Hashable {
    var id: String { name + "-" + series }
    let name: String
    let series: String
    let version: String
}

struct ServiceInstance: Codable, Identifiable, Hashable {
    var id: String { engine + "-" + version }
    let engine: String
    let version: String
    let port: Int
    let api_key: String?

    var title: String { Runtimes.title(engine) + " " + version }
}

struct ServiceSettings: Codable {
    let databases: [ServiceInstance]?
    let cache: ServiceInstance?
    let search: ServiceInstance?
    let mail: Bool
}

enum Runtimes {
    static let order = ["php", "mariadb", "mysql", "postgres", "valkey", "mailpit", "typesense"]
    static let databaseEngines = ["mariadb", "mysql", "postgres"]

    static func title(_ name: String) -> String {
        switch name {
        case "php": return "PHP"
        case "mariadb": return "MariaDB"
        case "mysql": return "MySQL"
        case "postgres": return "PostgreSQL"
        case "valkey": return "Valkey (compatível com Redis)"
        case "mailpit": return "Mailpit (email local)"
        case "typesense": return "Typesense (pesquisa)"
        default: return name
        }
    }

    static func shortTitle(_ name: String) -> String {
        switch name {
        case "valkey": return "Valkey"
        case "mailpit": return "Mailpit"
        case "typesense": return "Typesense"
        default: return title(name)
        }
    }
}

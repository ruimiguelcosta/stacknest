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
    let running: Bool
    let port: Int?
    let web_url: String?
    let log: String?
    let error: String?

    var title: String {
        switch name {
        case "mariadb": return "MariaDB"
        case "mailpit": return "Mailpit (email local)"
        default: return name.capitalized
        }
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
    let log: String?
    let error: String?

    var url: URL? { domain.flatMap { URL(string: "https://\($0)") } }
    var displayName: String { domain ?? name }

    static func == (a: ProjectState, b: ProjectState) -> Bool { a.name == b.name }
    func hash(into hasher: inout Hasher) { hasher.combine(name) }
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

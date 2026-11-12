import AppKit
import SwiftUI

extension Severity {
    var color: Color {
        switch self {
        case .CRITICAL: return .purple
        case .HIGH: return .red
        case .MEDIUM: return .orange
        case .LOW: return .yellow
        case .UNKNOWN: return .gray
        }
    }
}

struct SeverityBadge: View {
    let severity: Severity

    var body: some View {
        Text(severity.label)
            .font(.caption.weight(.semibold))
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .background(severity.color.opacity(0.18), in: Capsule())
            .foregroundStyle(severity.color)
            .frame(minWidth: 70)
    }
}

struct StatusDot: View {
    let ok: Bool
    var body: some View {
        Circle().fill(ok ? Color.green : Color.red).frame(width: 8, height: 8)
    }
}

/// A finding as a row: severity, identifier, affected component and summary.
struct FindingRow: View {
    let finding: Finding
    var projects: [String] = []

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            SeverityBadge(severity: finding.severity)
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 6) {
                    Link(finding.id, destination: URL(string: finding.url)!)
                        .font(.body.monospaced())
                    if let score = finding.score, score > 0 {
                        Text(String(format: "CVSS %.1f", score)).font(.caption).foregroundStyle(.secondary)
                    }
                    Spacer()
                    if finding.published > Date(timeIntervalSince1970: 0) {
                        Text(finding.published, format: .dateTime.day().month(.abbreviated).year())
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
                Text(finding.summary).lineLimit(2)
                HStack(spacing: 4) {
                    Text("\(finding.component) \(finding.version)").font(.caption.monospaced())
                    if let fixed = finding.fixed, !fixed.isEmpty {
                        Text("→ corrigido em \(fixed)").font(.caption).foregroundStyle(.green)
                    }
                }
                if !projects.isEmpty {
                    Text(projects.sorted().joined(separator: " · "))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(2)
                }
            }
        }
        .padding(.vertical, 4)
    }
}

struct StatCard: View {
    let title: String
    let value: String
    var color: Color = .primary
    var icon: String

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Label(title, systemImage: icon).font(.caption).foregroundStyle(.secondary)
            Text(value).font(.title2.weight(.semibold)).foregroundStyle(color)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(12)
        .background(.background.secondary, in: RoundedRectangle(cornerRadius: 10))
    }
}

enum Finder {
    static func open(_ path: String) {
        NSWorkspace.shared.open(URL(fileURLWithPath: path))
    }

    static func reveal(_ path: String) {
        NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: path)])
    }

    static func terminal(_ path: String) {
        let terminal = URL(fileURLWithPath: "/System/Applications/Utilities/Terminal.app")
        NSWorkspace.shared.open([URL(fileURLWithPath: path)], withApplicationAt: terminal, configuration: NSWorkspace.OpenConfiguration())
    }

    /// Opens a log in Console, which follows new lines as they are written.
    static func log(_ path: String) {
        let console = URL(fileURLWithPath: "/System/Applications/Utilities/Console.app")
        if FileManager.default.fileExists(atPath: path) {
            NSWorkspace.shared.open([URL(fileURLWithPath: path)], withApplicationAt: console, configuration: NSWorkspace.OpenConfiguration())
        }
    }
}

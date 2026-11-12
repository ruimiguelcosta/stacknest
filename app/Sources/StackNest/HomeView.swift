import SwiftUI

/// The home page: the newest vulnerabilities affecting what each project runs.
struct HomeView: View {
    @Environment(AppStore.self) private var store
    let openProject: (ProjectState) -> Void

    @State private var onlySerious = false
    @State private var search = ""

    private var findings: [AggregatedFinding] {
        store.latestFindings.filter { item in
            (!onlySerious || item.finding.severity <= .HIGH)
                && (search.isEmpty
                    || item.finding.id.localizedCaseInsensitiveContains(search)
                    || item.finding.component.localizedCaseInsensitiveContains(search)
                    || item.finding.summary.localizedCaseInsensitiveContains(search)
                    || item.projects.contains { $0.localizedCaseInsensitiveContains(search) })
        }
    }

    var body: some View {
        let all = store.latestFindings.map(\.finding)
        let counts = store.severityCounts(all)
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                header
                HStack(spacing: 12) {
                    StatCard(title: "Sites a correr", value: "\(store.projects.filter(\.running).count) / \(store.projects.count)", icon: "globe")
                    StatCard(title: "Críticas", value: "\(counts[.CRITICAL] ?? 0)", color: Severity.CRITICAL.color, icon: "exclamationmark.octagon")
                    StatCard(title: "Altas", value: "\(counts[.HIGH] ?? 0)", color: Severity.HIGH.color, icon: "exclamationmark.triangle")
                    StatCard(title: "Médias e baixas", value: "\((counts[.MEDIUM] ?? 0) + (counts[.LOW] ?? 0) + (counts[.UNKNOWN] ?? 0))", color: Severity.MEDIUM.color, icon: "info.circle")
                }
                runtimes
                projectSummary
                latest
            }
            .padding(20)
        }
        .searchable(text: $search, prompt: "Procurar CVE, pacote ou projeto")
    }

    private var header: some View {
        HStack(alignment: .firstTextBaseline) {
            VStack(alignment: .leading, spacing: 4) {
                Text("Segurança").font(.largeTitle.weight(.semibold))
                Group {
                    if let report = store.cveReport {
                        Text("Vulnerabilidades publicadas que afetam as versões instaladas · verificado \(report.generated, format: .relative(presentation: .named))")
                    } else {
                        Text("Vulnerabilidades publicadas que afetam as versões instaladas")
                    }
                }
                .foregroundStyle(.secondary)
            }
            Spacer()
            if store.cveLoading {
                ProgressView().controlSize(.small)
                Text("A consultar OSV e NVD…").foregroundStyle(.secondary)
            }
            Button {
                Task { await store.loadCVEs(refresh: true) }
            } label: {
                Label("Verificar agora", systemImage: "arrow.clockwise")
            }
            .disabled(store.cveLoading)
        }
    }

    @ViewBuilder private var runtimes: some View {
        if let runtimes = store.cveReport?.runtimes, !runtimes.isEmpty {
            HStack(spacing: 12) {
                ForEach(runtimes) { runtime in
                    HStack {
                        Image(systemName: runtime.findings.isEmpty ? "checkmark.seal.fill" : "exclamationmark.shield.fill")
                            .foregroundStyle(runtime.findings.isEmpty ? .green : .red)
                        VStack(alignment: .leading) {
                            Text("\(runtime.component) \(runtime.version)").font(.headline)
                            Text(runtime.error ?? (runtime.findings.isEmpty ? "Sem CVEs conhecidas" : "\(runtime.findings.count) CVEs"))
                                .font(.caption)
                                .foregroundStyle(runtime.error == nil ? Color.secondary : Color.red)
                                .lineLimit(1)
                        }
                        Spacer()
                        Text("\(runtime.projects?.count ?? 0) projetos").font(.caption).foregroundStyle(.secondary)
                    }
                    .padding(10)
                    .background(.background.secondary, in: RoundedRectangle(cornerRadius: 10))
                }
            }
        }
    }

    @ViewBuilder private var projectSummary: some View {
        let reports = (store.cveReport?.projects ?? []).filter { !$0.findings.isEmpty || $0.error != nil }
            .sorted { worst($0) == worst($1) ? $0.findings.count > $1.findings.count : worst($0) < worst($1) }
        if !reports.isEmpty {
            VStack(alignment: .leading, spacing: 8) {
                Text("Projetos afetados").font(.title3.weight(.semibold))
                LazyVGrid(columns: [GridItem(.adaptive(minimum: 230), spacing: 10)], spacing: 10) {
                    ForEach(reports) { report in
                        Button {
                            if let project = store.projects.first(where: { $0.name == report.name }) { openProject(project) }
                        } label: {
                            projectCard(report)
                        }
                        .buttonStyle(.plain)
                    }
                }
            }
        }
    }

    private func worst(_ report: ProjectReport) -> Severity {
        report.findings.map(\.severity).min() ?? .UNKNOWN
    }

    private func projectCard(_ report: ProjectReport) -> some View {
        let counts = store.severityCounts(report.findings)
        return VStack(alignment: .leading, spacing: 6) {
            Text(report.domain).font(.headline).lineLimit(1)
            if let error = report.error {
                Text(error).font(.caption).foregroundStyle(.red).lineLimit(2)
            }
            HStack(spacing: 6) {
                ForEach(Severity.allCases, id: \.self) { severity in
                    if let count = counts[severity] {
                        Text("\(count) \(severity.label.lowercased())")
                            .font(.caption)
                            .foregroundStyle(severity.color)
                    }
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(10)
        .background(.background.secondary, in: RoundedRectangle(cornerRadius: 10))
    }

    @ViewBuilder private var latest: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text("CVEs mais recentes").font(.title3.weight(.semibold))
                Spacer()
                Toggle("Só críticas e altas", isOn: $onlySerious).toggleStyle(.switch).controlSize(.small)
            }
            if let error = store.cveError {
                Label(error, systemImage: "wifi.exclamationmark").foregroundStyle(.red)
            } else if store.cveReport == nil {
                Text(store.cveLoading ? "A preparar o primeiro relatório…" : "Sem relatório.").foregroundStyle(.secondary)
            } else if findings.isEmpty {
                Label("Nenhuma vulnerabilidade conhecida afeta os teus projetos.", systemImage: "checkmark.seal")
                    .foregroundStyle(.green)
            } else {
                LazyVStack(alignment: .leading, spacing: 0) {
                    ForEach(findings) { item in
                        FindingRow(finding: item.finding, projects: item.projects)
                        Divider()
                    }
                }
            }
        }
    }
}

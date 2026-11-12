import SwiftUI

/// Every PHP, database and service version StackNest can install.
struct RuntimesView: View {
    @Environment(AppStore.self) private var store

    var body: some View {
        Form {
            if let error = store.catalogError {
                Section {
                    Label("Não foi possível obter a lista de versões: \(error)", systemImage: "wifi.exclamationmark")
                        .foregroundStyle(.red)
                    Button("Tentar outra vez") { Task { await store.loadRuntimes() } }
                }
            }
            ForEach(Runtimes.order, id: \.self) { name in
                let entries = store.catalogEntries(name)
                let orphans = store.installed.filter { item in item.name == name && !entries.contains { $0.series == item.series } }
                if !entries.isEmpty || !orphans.isEmpty {
                    Section(Runtimes.title(name)) {
                        ForEach(entries) { entry in
                            RuntimeRow(name: name, series: entry.series, available: entry)
                        }
                        ForEach(orphans) { item in
                            RuntimeRow(name: name, series: item.series, available: nil)
                        }
                    }
                }
            }
        }
        .formStyle(.grouped)
        .task { await store.loadRuntimes() }
    }
}

struct RuntimeRow: View {
    @Environment(AppStore.self) private var store
    let name: String
    let series: String
    let available: CatalogEntry?
    @State private var confirmRemove = false

    private var installed: InstalledRuntime? { store.installedRuntime(name, series) }
    private var key: String { name + "-" + series }

    var body: some View {
        HStack {
            VStack(alignment: .leading, spacing: 2) {
                Text("\(Runtimes.shortTitle(name)) \(series)").font(.headline)
                Text(detail).font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
            if let value = store.progress[key] {
                ProgressView(value: value).frame(width: 140)
                Text("\(Int(value * 100))%").font(.caption.monospacedDigit()).frame(width: 36)
            } else if let installed {
                if let available, !installed.version.isEmpty, installed.version != available.version {
                    Button("Atualizar para \(available.version)") { Task { await store.installRuntime(name, series) } }
                }
                Button("Remover", role: .destructive) { confirmRemove = true }
            } else if available != nil {
                Button("Instalar") { Task { await store.installRuntime(name, series) } }
            }
        }
        .confirmationDialog("Remover \(Runtimes.shortTitle(name)) \(series)?", isPresented: $confirmRemove) {
            Button("Remover", role: .destructive) { Task { await store.removeRuntime(name, series) } }
        } message: {
            Text("Os dados das bases de dados e dos serviços ficam guardados.")
        }
    }

    private var detail: String {
        var parts: [String] = []
        if let installed {
            parts.append(installed.version.isEmpty ? "Instalado" : "Instalado: \(installed.version)")
        } else {
            parts.append("Não instalado")
        }
        if let available {
            var text = "disponível: \(available.version)"
            if let size = available.size { text += " (\(size / 1_048_576) MB)" }
            parts.append(text)
        }
        return parts.joined(separator: " · ")
    }
}

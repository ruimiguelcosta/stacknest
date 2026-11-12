import AppKit
import SwiftUI

struct ServicesView: View {
    @Environment(AppStore.self) private var store
    @State private var addingDatabase = false
    @State private var removing: ServiceInstance?

    var body: some View {
        Form {
            Section {
                ForEach(store.databaseInstances) { instance in
                    serviceRow(id: instance.id, title: instance.title, detail: databaseDetail(instance)) {
                        Button("Remover", role: .destructive) { removing = instance }
                    }
                }
                if store.databaseInstances.isEmpty {
                    Text("Nenhum servidor. Os projetos podem usar SQLite, que não precisa de servidor.")
                        .foregroundStyle(.secondary)
                }
                Button {
                    addingDatabase = true
                } label: {
                    Label("Adicionar servidor de base de dados", systemImage: "plus")
                }
            } header: {
                Text("Bases de dados")
            } footer: {
                Text("Utilizador root (MariaDB e MySQL) ou postgres, sem password, só acessível a partir deste Mac.")
                    .font(.caption).foregroundStyle(.secondary)
            }

            Section("Outros serviços") {
                optionalService("mailpit", enabled: store.settings?.mail == true, instance: nil,
                                detail: "SMTP 127.0.0.1:2525 · caixa de correio em https://mail.test")
                optionalService("valkey", enabled: store.settings?.cache != nil, instance: store.settings?.cache,
                                detail: store.settings?.cache.map { "127.0.0.1:\($0.port) · REDIS_HOST=127.0.0.1 REDIS_PORT=\($0.port)" } ?? "Cache e filas compatíveis com Redis")
                optionalService("typesense", enabled: store.settings?.search != nil, instance: store.settings?.search,
                                detail: store.settings?.search.map { "127.0.0.1:\($0.port) · chave API \($0.api_key ?? "")" } ?? "Pesquisa para o Laravel Scout")
            }

            Section {
                LabeledContent("Domínios .test") { systemState(store.resolverConfigured) }
                LabeledContent("Certificado HTTPS de confiança") { systemState(store.caTrusted) }
                if !store.systemReady {
                    Button("Configurar (pede a password de administrador)") {
                        Task { await store.configureSystem() }
                    }
                }
            } header: {
                Text("Sistema")
            } footer: {
                Text("O StackNest responde aos domínios .test na porta 5354 e emite certificados com uma autoridade local, guardada em ~/.stacknest/caddy.")
                    .font(.caption).foregroundStyle(.secondary)
            }

            Section("StackNest") {
                if let status = store.status {
                    LabeledContent("Processo", value: "PID \(status.pid)")
                    LabeledContent("Última atualização") {
                        Text(status.updated, format: .relative(presentation: .named))
                    }
                    if let webError = status.web_error {
                        Text(webError).foregroundStyle(.red)
                    }
                }
                HStack {
                    Button("Reiniciar tudo") { Task { await store.restart() } }.disabled(store.busy)
                    Button("Registo do StackNest") { Finder.log(CLI.home.appendingPathComponent("logs/stacknest.log").path) }
                    Button("Abrir pasta ~/.stacknest") { Finder.open(CLI.home.path) }
                }
            }
        }
        .formStyle(.grouped)
        .task {
            await store.loadRuntimes()
            await store.checkSystem()
        }
        .sheet(isPresented: $addingDatabase) { AddDatabaseSheet() }
        .confirmationDialog("Remover \(removing?.title ?? "")?", isPresented: Binding(get: { removing != nil }, set: { if !$0 { removing = nil } })) {
            Button("Remover", role: .destructive) {
                if let instance = removing { Task { await store.disableService(instance.engine, version: instance.version) } }
            }
        } message: {
            Text("O servidor deixa de correr. Os dados ficam guardados em ~/.stacknest/data e voltam se o adicionares outra vez.")
        }
    }

    private func state(_ id: String) -> ServiceState? { store.services.first { $0.name == id } }

    private func databaseDetail(_ instance: ServiceInstance) -> String {
        let user = instance.engine == "postgres" ? "postgres" : "root"
        return "127.0.0.1:\(instance.port) · utilizador \(user)"
    }

    @ViewBuilder
    private func serviceRow<Actions: View>(id: String, title: String, detail: String, @ViewBuilder actions: () -> Actions) -> some View {
        let current = state(id)
        HStack(alignment: .top) {
            StatusDot(ok: current?.running == true).padding(.top, 5)
            VStack(alignment: .leading, spacing: 2) {
                Text(title).font(.headline)
                if let error = current?.error {
                    Text(error).font(.caption).foregroundStyle(.red)
                } else {
                    Text(detail).font(.caption).foregroundStyle(.secondary).textSelection(.enabled)
                }
            }
            Spacer()
            if let url = current?.web_url.flatMap(URL.init) {
                Button("Abrir") { NSWorkspace.shared.open(url) }
            }
            if let log = current?.log {
                Button("Registo") { Finder.log(log) }
            }
            actions()
        }
    }

    @ViewBuilder
    private func optionalService(_ name: String, enabled: Bool, instance: ServiceInstance?, detail: String) -> some View {
        let id = name == "mailpit" ? "mailpit" : (instance?.id ?? name)
        let installing = store.progress.keys.contains { $0.hasPrefix(name + "-") }
        if enabled {
            serviceRow(id: id, title: Runtimes.title(name) + (instance.map { " " + $0.version } ?? ""), detail: detail) {
                Button("Desligar") { Task { await store.disableService(name) } }
            }
        } else {
            HStack {
                StatusDot(ok: false).opacity(0.3)
                VStack(alignment: .leading, spacing: 2) {
                    Text(Runtimes.title(name)).font(.headline)
                    Text(detail).font(.caption).foregroundStyle(.secondary)
                }
                Spacer()
                if installing {
                    ProgressView().controlSize(.small)
                } else {
                    Button("Ligar") {
                        Task { if let error = await store.enableService(name) { store.alert = error } }
                    }
                }
            }
        }
    }

    private func systemState(_ ok: Bool) -> some View {
        Label(ok ? "Configurado" : "Por configurar", systemImage: ok ? "checkmark.circle.fill" : "xmark.circle")
            .foregroundStyle(ok ? .green : .orange)
    }
}

/// Picks an engine, version and port for a new database server.
struct AddDatabaseSheet: View {
    @Environment(AppStore.self) private var store
    @Environment(\.dismiss) private var dismiss
    @State private var engine = "mariadb"
    @State private var series = ""
    @State private var port = ""
    @State private var error: String?
    @State private var working = false

    private var versions: [CatalogEntry] { store.catalogEntries(engine) }

    var body: some View {
        Form {
            Text("Adicionar servidor de base de dados").font(.headline)
            Picker("Motor", selection: $engine) {
                ForEach(Runtimes.databaseEngines, id: \.self) { Text(Runtimes.title($0)).tag($0) }
            }
            .onChange(of: engine) { _, _ in series = versions.first?.series ?? "" }
            Picker("Versão", selection: $series) {
                ForEach(versions) { entry in
                    Text("\(entry.series) (\(entry.version))").tag(entry.series)
                }
            }
            TextField("Porta", text: $port, prompt: Text("automática"))
            if store.installedRuntime(engine, series) == nil, let size = versions.first(where: { $0.series == series })?.size {
                Text("Vai ser transferido (\(size / 1_048_576) MB).").font(.caption).foregroundStyle(.secondary)
            }
            if let value = store.progress[engine + "-" + series] {
                ProgressView(value: value)
            }
            if let error { Text(error).foregroundStyle(.red) }
            HStack {
                Spacer()
                Button("Cancelar") { dismiss() }.disabled(working)
                Button("Adicionar") {
                    Task {
                        working = true
                        error = await store.enableService(engine, version: series, port: Int(port))
                        working = false
                        if error == nil { dismiss() }
                    }
                }
                .keyboardShortcut(.defaultAction)
                .disabled(series.isEmpty || working)
            }
        }
        .formStyle(.grouped)
        .frame(width: 460)
        .onAppear { series = versions.first?.series ?? "" }
    }
}

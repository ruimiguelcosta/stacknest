import SwiftUI

/// First run: installs the engine and the chosen runtimes, then sets up .test and HTTPS.
struct OnboardingView: View {
    @Environment(AppStore.self) private var store
    @Environment(\.dismiss) private var dismiss

    @State private var php: Set<String> = []
    @State private var database = "mariadb"
    @State private var databaseSeries = ""
    @State private var mail = true
    @State private var cache = true
    @State private var search = false
    @State private var steps: [Step] = []
    @State private var running = false
    @State private var finished = false

    struct Step: Identifiable {
        let id: String
        let title: String
        var state: State = .waiting
        enum State { case waiting, running, done, failed(String) }
    }

    private var phpEntries: [CatalogEntry] { store.catalogEntries("php") }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack(spacing: 12) {
                Image(systemName: "shippingbox.fill").font(.largeTitle).foregroundStyle(.teal)
                VStack(alignment: .leading) {
                    Text("Bem-vindo ao StackNest").font(.title.weight(.semibold))
                    Text("Escolhe o que queres instalar. Tudo fica em ~/.stacknest e não mexe no resto do sistema.")
                        .foregroundStyle(.secondary)
                }
            }
            if steps.isEmpty {
                choices
            } else {
                progressList
            }
            HStack {
                if !running && !finished {
                    Button("Agora não") { dismiss() }
                }
                Spacer()
                if finished {
                    Button("Começar") { dismiss() }.keyboardShortcut(.defaultAction)
                } else {
                    Button("Instalar") { Task { await install() } }
                        .keyboardShortcut(.defaultAction)
                        .disabled(running || php.isEmpty)
                }
            }
        }
        .padding(24)
        .frame(width: 560)
        .interactiveDismissDisabled(running)
        .task {
            if store.catalog.isEmpty { await store.loadRuntimes() }
            php = Set(phpEntries.prefix(2).map(\.series))
            databaseSeries = store.catalogEntries(database).first?.series ?? ""
        }
    }

    @ViewBuilder private var choices: some View {
        Form {
            if let error = store.catalogError {
                Text("Sem ligação ao catálogo de versões: \(error)").foregroundStyle(.red)
            }
            Section("PHP") {
                ForEach(phpEntries) { entry in
                    Toggle("PHP \(entry.series) (\(entry.version))", isOn: Binding(
                        get: { php.contains(entry.series) },
                        set: { if $0 { php.insert(entry.series) } else { php.remove(entry.series) } }
                    ))
                }
            }
            Section("Base de dados") {
                Picker("Servidor", selection: $database) {
                    Text("Nenhum (SQLite)").tag("")
                    ForEach(Runtimes.databaseEngines, id: \.self) { Text(Runtimes.title($0)).tag($0) }
                }
                .onChange(of: database) { _, _ in databaseSeries = store.catalogEntries(database).first?.series ?? "" }
                if !database.isEmpty {
                    Picker("Versão", selection: $databaseSeries) {
                        ForEach(store.catalogEntries(database)) { Text("\($0.series) (\($0.version))").tag($0.series) }
                    }
                }
            }
            Section("Serviços") {
                Toggle("Caixa de correio local (Mailpit)", isOn: $mail)
                Toggle("Valkey, compatível com Redis", isOn: $cache)
                Toggle("Pesquisa com Typesense", isOn: $search)
            }
        }
        .formStyle(.grouped)
        .frame(height: 380)
    }

    private var progressList: some View {
        VStack(alignment: .leading, spacing: 10) {
            ForEach(steps) { step in
                HStack {
                    switch step.state {
                    case .waiting: Image(systemName: "circle").foregroundStyle(.secondary)
                    case .running: ProgressView().controlSize(.small)
                    case .done: Image(systemName: "checkmark.circle.fill").foregroundStyle(.green)
                    case .failed: Image(systemName: "xmark.circle.fill").foregroundStyle(.red)
                    }
                    VStack(alignment: .leading) {
                        Text(step.title)
                        if case .failed(let message) = step.state {
                            Text(message).font(.caption).foregroundStyle(.red)
                        }
                        if case .running = step.state, let value = store.progress[step.id] {
                            ProgressView(value: value).frame(width: 300)
                        }
                    }
                }
            }
        }
        .frame(minHeight: 200, alignment: .top)
    }

    private func install() async {
        running = true
        var plan: [(Step, () async -> String?)] = []
        if !CLI.isInstalled || store.engineUpdateAvailable {
            plan.append((Step(id: "engine", title: "Instalar o serviço do StackNest"), {
                await store.installEngine()
                return CLI.isInstalled ? nil : (store.alert ?? "Falhou.")
            }))
        }
        for series in phpEntries.map(\.series) where php.contains(series) {
            plan.append((Step(id: "php-" + series, title: "PHP \(series)"), {
                await store.runWithProgress(["runtime", "install", "php", series], key: "php-" + series)
            }))
        }
        if !database.isEmpty {
            let engine = database, series = databaseSeries
            plan.append((Step(id: engine + "-" + series, title: "\(Runtimes.title(engine)) \(series)"), {
                await store.enableService(engine, version: series)
            }))
        }
        let optional: [(String, Bool)] = [("mailpit", mail), ("valkey", cache), ("typesense", search)]
        for (name, wanted) in optional {
            if wanted {
                plan.append((Step(id: name + "-", title: Runtimes.title(name)), { await store.enableService(name) }))
            } else {
                plan.append((Step(id: name + "-off", title: "\(Runtimes.title(name)): desligado"), {
                    await store.disableService(name)
                    return nil
                }))
            }
        }
        plan.append((Step(id: "system", title: "Domínios .test e certificado HTTPS (pede a password)"), {
            await store.configureSystem()
            return store.systemReady ? nil : "Podes fazer isto mais tarde em Serviços."
        }))

        steps = plan.map(\.0)
        for (index, item) in plan.enumerated() {
            steps[index].state = .running
            if let error = await item.1() {
                steps[index].state = .failed(error)
            } else {
                steps[index].state = .done
            }
        }
        running = false
        finished = true
    }
}

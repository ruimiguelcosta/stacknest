import AppKit
import SwiftUI

struct ProjectsView: View {
    @Environment(AppStore.self) private var store
    @Binding var selection: ProjectState?
    @State private var search = ""
    @State private var linking: URL?

    private var filtered: [ProjectState] {
        store.projects.filter {
            search.isEmpty || $0.displayName.localizedCaseInsensitiveContains(search) || $0.dir.localizedCaseInsensitiveContains(search)
        }
    }

    var body: some View {
        HSplitView {
            List(filtered, selection: $selection) { project in
                HStack {
                    StatusDot(ok: project.running)
                    VStack(alignment: .leading) {
                        Text(project.displayName).lineLimit(1)
                        Text("PHP \(project.php ?? "?")").font(.caption).foregroundStyle(.secondary)
                    }
                    Spacer()
                    let serious = store.findings(for: project).filter { $0.severity <= .HIGH }.count
                    if serious > 0 {
                        Text("\(serious)")
                            .font(.caption.weight(.bold))
                            .padding(.horizontal, 6)
                            .background(Color.red.opacity(0.2), in: Capsule())
                            .foregroundStyle(.red)
                            .help("CVEs críticas ou altas")
                    }
                }
                .tag(project)
            }
            .searchable(text: $search, placement: .sidebar, prompt: "Procurar")
            .frame(minWidth: 240, idealWidth: 270, maxWidth: 360)
            .safeAreaInset(edge: .bottom) {
                Button {
                    chooseFolder()
                } label: {
                    Label("Adicionar pasta existente", systemImage: "plus")
                        .frame(maxWidth: .infinity)
                }
                .padding(8)
                .background(.bar)
            }

            Group {
                if let current = selection, let project = store.projects.first(where: { $0.name == current.name }) {
                    ProjectDetailView(project: project)
                        .id(project.name)
                } else {
                    ContentUnavailableView("Escolhe um projeto", systemImage: "folder", description: Text("\(store.projects.count) projetos registados"))
                }
            }
            .frame(minWidth: 480, maxWidth: .infinity, maxHeight: .infinity)
        }
        .sheet(item: $linking) { folder in
            LinkSheet(folder: folder)
        }
    }

    private func chooseFolder() {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.prompt = "Adicionar"
        if panel.runModal() == .OK, let url = panel.url {
            linking = url
        }
    }
}

extension URL: @retroactive Identifiable {
    public var id: String { absoluteString }
}

struct ProjectDetailView: View {
    @Environment(AppStore.self) private var store
    let project: ProjectState

    @State private var php = ""
    @State private var domain = ""
    @State private var aliases = ""
    @State private var opcache = OPcache(enabled: true, memory: 256, validate_timestamps: true, jit: false)
    @State private var error: String?
    @State private var needsForce = false
    @State private var confirmUnlink = false
    @State private var saved = false
    @State private var xdebug = false
    @State private var dbInstance: ServiceInstance?
    @State private var dbName = ""
    @State private var servicesMessage: (ok: Bool, text: String)?
    @State private var receivingMail = false

    private let memoryOptions = [64, 128, 256, 512, 1024]

    var body: some View {
        Form {
            Section {
                HStack(spacing: 10) {
                    StatusDot(ok: project.running)
                    Text(project.displayName).font(.title2.weight(.semibold))
                    Spacer()
                    if let url = project.url {
                        Button("Abrir no browser") { NSWorkspace.shared.open(url) }
                            .keyboardShortcut(.defaultAction)
                    }
                }
                if let error = project.error {
                    Label(error, systemImage: "exclamationmark.triangle").foregroundStyle(.red)
                }
                HStack {
                    Button("Mostrar no Finder") { Finder.reveal(project.dir) }
                    Button("Abrir no Terminal") { Finder.terminal(project.dir) }
                    if let log = project.log { Button("Registo do PHP") { Finder.log(log) } }
                }
            }

            Section("Site") {
                TextField("Domínio", text: $domain)
                TextField("Domínios adicionais", text: $aliases, prompt: Text("www.app.test, *.app.test"))
                Picker("PHP", selection: $php) {
                    ForEach(store.phpVersions) { version in
                        Text("PHP \(version.version) (\(version.full))").tag(version.version)
                    }
                }
                LabeledContent("Pasta", value: project.dir)
                if let root = project.root, root != project.dir {
                    LabeledContent("Raiz pública", value: root)
                }
                if project.static == true {
                    LabeledContent("Tipo", value: "Site estático (sem PHP)")
                }
                if project.custom == true {
                    LabeledContent("Regras", value: "Personalizadas em .stacknest.yaml")
                }
            }

            Section {
                Toggle("Ativar OPcache", isOn: $opcache.enabled)
                Picker("Memória", selection: $opcache.memory) {
                    ForEach(memoryOptions + (memoryOptions.contains(opcache.memory) ? [] : [opcache.memory]), id: \.self) { value in
                        Text("\(value) MB").tag(value)
                    }
                }
                .disabled(!opcache.enabled)
                Toggle("Detetar alterações nos ficheiros", isOn: $opcache.validate_timestamps)
                    .disabled(!opcache.enabled)
                Toggle("JIT", isOn: $opcache.jit)
                    .disabled(!opcache.enabled)
            } header: {
                Text("OPcache")
            } footer: {
                Text("Cada projeto tem o seu próprio PHP-FPM, por isso a memória do OPcache não é partilhada. Desligar a deteção de alterações acelera o site, mas as edições ao código só aparecem depois de reiniciar.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            Section {
                Toggle("Xdebug", isOn: $xdebug)
                    .disabled(!store.xdebugAvailable(php: php) && !xdebug)
            } header: {
                Text("Depuração")
            } footer: {
                Text(store.xdebugAvailable(php: php)
                     ? "Liga-se só nos pedidos que o pedem (extensão do browser ou XDEBUG_TRIGGER), na porta 9003, por isso não torna o site mais lento."
                     : "Esta versão de PHP ainda não inclui o Xdebug.")
                    .font(.caption).foregroundStyle(.secondary)
            }

            Section {
                HStack {
                    if let error {
                        Text(error).foregroundStyle(.red).font(.callout).textSelection(.enabled)
                    } else if saved {
                        Label("Guardado. O StackNest aplica em poucos segundos.", systemImage: "checkmark").foregroundStyle(.green)
                    }
                    Spacer()
                    if needsForce {
                        Button("Mudar mesmo assim") { Task { await save(force: true) } }
                    }
                    Button("Guardar") { Task { await save(force: false) } }
                        .disabled(store.busy || arguments(force: false).isEmpty)
                }
            }

            Section {
                if let current = project.database {
                    LabeledContent("Servidor atual", value: "\(Runtimes.title(current.engine)) \(current.version)")
                }
                if store.databaseInstances.isEmpty {
                    Text("Não há servidores de base de dados. Adiciona um em Serviços.").foregroundStyle(.secondary)
                } else {
                    Picker("Servidor", selection: $dbInstance) {
                        ForEach(store.databaseInstances) { Text("\($0.title) · porta \($0.port)").tag(Optional($0)) }
                    }
                    TextField("Nome da base de dados", text: $dbName)
                    HStack {
                        Spacer()
                        Button("Criar e ligar no .env") {
                            Task {
                                guard let instance = dbInstance else { return }
                                let result = await store.createDatabase(for: project, instance: instance, name: dbName, writeEnv: true)
                                servicesMessage = (result.ok, result.message)
                            }
                        }
                        .disabled(dbInstance == nil || store.busy)
                    }
                }
                HStack {
                    Button("Email para a caixa local") { Task { await env(mail: true) } }
                    Button("Redis → Valkey") { Task { await env(cache: true) } }
                        .disabled(store.settings?.cache == nil)
                    Button("Scout → Typesense") { Task { await env(search: true) } }
                        .disabled(store.settings?.search == nil)
                    Spacer()
                    Button("Simular email recebido…") { receivingMail = true }
                }
                if let message = servicesMessage {
                    Text(message.text).font(.callout).foregroundStyle(message.ok ? Color.green : Color.red).textSelection(.enabled)
                }
            } header: {
                Text("Serviços do projeto")
            } footer: {
                Text("O StackNest guarda uma cópia do .env em ~/.stacknest/backups antes de o alterar.")
                    .font(.caption).foregroundStyle(.secondary)
            }

            let findings = store.findings(for: project)
            Section("Vulnerabilidades (\(findings.count))") {
                if findings.isEmpty {
                    Label(store.cveReport == nil ? "Relatório ainda não disponível." : "Sem vulnerabilidades conhecidas nas versões instaladas.", systemImage: "checkmark.seal")
                        .foregroundStyle(.secondary)
                } else {
                    ForEach(findings, id: \.self) { finding in
                        FindingRow(finding: finding)
                    }
                }
            }

            Section {
                Button("Deixar de servir este projeto…", role: .destructive) { confirmUnlink = true }
            } footer: {
                Text("A pasta e o ficheiro .stacknest.yaml ficam intactos.").font(.caption).foregroundStyle(.secondary)
            }
        }
        .formStyle(.grouped)
        .onAppear(perform: load)
        .sheet(isPresented: $receivingMail) { InboundMailSheet(project: project) }
        .confirmationDialog("Deixar de servir \(project.displayName)?", isPresented: $confirmUnlink) {
            Button("Deixar de servir", role: .destructive) { Task { await store.unlink(project) } }
        }
    }

    private func load() {
        php = project.php ?? ""
        domain = project.domain ?? ""
        aliases = (project.aliases ?? []).joined(separator: ", ")
        opcache = project.opcache
        xdebug = project.xdebug ?? false
        dbName = project.name.replacingOccurrences(of: "-", with: "_")
        let current = project.database
        dbInstance = store.databaseInstances.first { $0.engine == current?.engine && $0.version == current?.version }
            ?? store.databaseInstances.first
    }

    private func env(mail: Bool = false, cache: Bool = false, search: Bool = false) async {
        let result = await store.applyEnv(to: project, mail: mail, cache: cache, search: search)
        servicesMessage = (result.ok, result.message)
    }

    private func arguments(force: Bool) -> [String] {
        var args: [String] = []
        if php != project.php { args += ["--php", php] }
        if domain != project.domain { args += ["--domain", domain] }
        let currentAliases = (project.aliases ?? []).joined(separator: ", ")
        if aliases != currentAliases { args += ["--aliases", aliases] }
        if opcache.enabled != project.opcache.enabled { args.append("--opcache=\(opcache.enabled)") }
        if opcache.memory != project.opcache.memory { args += ["--opcache-memory", String(opcache.memory)] }
        if opcache.validate_timestamps != project.opcache.validate_timestamps { args.append("--validate-timestamps=\(opcache.validate_timestamps)") }
        if opcache.jit != project.opcache.jit { args.append("--jit=\(opcache.jit)") }
        if xdebug != (project.xdebug ?? false) { args.append("--xdebug=\(xdebug)") }
        if force && !args.isEmpty { args.append("--force") }
        return args
    }

    private func save(force: Bool) async {
        saved = false
        let message = await store.save(project, arguments: arguments(force: force))
        error = message
        needsForce = message?.contains("--force") == true
        if let message, needsForce {
            error = message.replacingOccurrences(of: "\n(use --force to change anyway)", with: "")
        }
        saved = message == nil
    }
}

/// Asks for the PHP version and domain of a folder being added.
struct LinkSheet: View {
    @Environment(AppStore.self) private var store
    @Environment(\.dismiss) private var dismiss
    let folder: URL

    @State private var php = ""
    @State private var domain = ""
    @State private var error: String?

    var body: some View {
        Form {
            Text("Adicionar \(folder.lastPathComponent)").font(.headline)
            LabeledContent("Pasta", value: folder.path)
            TextField("Domínio", text: $domain)
            Picker("PHP", selection: $php) {
                ForEach(store.phpVersions) { Text("PHP \($0.version)").tag($0.version) }
            }
            if let error { Text(error).foregroundStyle(.red) }
            HStack {
                Spacer()
                Button("Cancelar") { dismiss() }
                Button("Adicionar") {
                    Task {
                        error = await store.link(directory: folder, php: php, domain: domain)
                        if error == nil { dismiss() }
                    }
                }
                .keyboardShortcut(.defaultAction)
                .disabled(php.isEmpty || store.busy)
            }
        }
        .formStyle(.grouped)
        .frame(width: 480)
        .onAppear {
            php = store.phpVersions.first?.version ?? ""
            let base = folder.lastPathComponent.lowercased()
                .replacingOccurrences(of: "[^a-z0-9.-]", with: "-", options: .regularExpression)
            domain = base + ".test"
        }
    }
}

/// Posts a made-up email to a project, as an inbound mail service would.
struct InboundMailSheet: View {
    @Environment(AppStore.self) private var store
    @Environment(\.dismiss) private var dismiss
    let project: ProjectState

    @AppStorage("inboundFormat") private var format = "postmark"
    @State private var url = ""
    @State private var from = "cliente@example.test"
    @State private var to = ""
    @State private var subject = "Pedido de informação"
    @State private var text = "Olá, gostaria de saber mais sobre os vossos serviços."
    @State private var result: (ok: Bool, text: String)?
    @State private var sending = false

    var body: some View {
        Form {
            Text("Simular email recebido em \(project.displayName)").font(.headline)
            TextField("Endereço do webhook", text: $url, prompt: Text("https://app.test/inbound"))
            Picker("Formato", selection: $format) {
                Text("Postmark (JSON)").tag("postmark")
                Text("Mailgun (formulário)").tag("mailgun")
                Text("Email em bruto (MIME)").tag("raw")
            }
            TextField("De", text: $from)
            TextField("Para", text: $to)
            TextField("Assunto", text: $subject)
            TextField("Mensagem", text: $text, axis: .vertical).lineLimit(4...8)
            if let result {
                Text(result.text).font(.caption.monospaced()).foregroundStyle(result.ok ? Color.green : Color.red)
                    .textSelection(.enabled).lineLimit(8)
            }
            HStack {
                Spacer()
                Button("Fechar") { dismiss() }
                Button("Enviar") {
                    Task {
                        sending = true
                        let reply = await store.receiveMail(url: url, format: format, from: from, to: to, subject: subject, text: text)
                        result = (reply.ok, reply.message)
                        sending = false
                    }
                }
                .keyboardShortcut(.defaultAction)
                .disabled(url.isEmpty || to.isEmpty || sending)
            }
        }
        .formStyle(.grouped)
        .frame(width: 520)
        .onAppear {
            let domain = project.domain ?? "app.test"
            if url.isEmpty { url = "https://\(domain)/" }
            if to.isEmpty { to = "suporte@\(domain)" }
        }
    }
}

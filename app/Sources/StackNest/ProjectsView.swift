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
        .confirmationDialog("Deixar de servir \(project.displayName)?", isPresented: $confirmUnlink) {
            Button("Deixar de servir", role: .destructive) { Task { await store.unlink(project) } }
        }
    }

    private func load() {
        php = project.php ?? ""
        domain = project.domain ?? ""
        aliases = (project.aliases ?? []).joined(separator: ", ")
        opcache = project.opcache
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

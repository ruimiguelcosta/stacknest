import AppKit
import SwiftUI

/// Creates a Laravel project with Composer and serves it straight away.
struct NewProjectView: View {
    @Environment(AppStore.self) private var store
    let onCreated: () -> Void

    @AppStorage("newProjectParent") private var parentPath = ""
    @State private var name = ""
    @State private var major = 0
    @State private var php = ""
    @State private var allowInsecure = false
    @State private var database: ServiceInstance?
    @State private var useCache = false
    @State private var running = false
    @State private var output = ""
    @State private var finished: Bool?

    private var release: LaravelRelease? { store.laravelReleases.first { $0.major == major } }
    private var validName: Bool { name.range(of: "^[a-z0-9][a-z0-9-]*$", options: .regularExpression) != nil }
    private var parent: URL { URL(fileURLWithPath: parentPath.isEmpty ? defaultParent : parentPath) }
    private var exists: Bool { !name.isEmpty && FileManager.default.fileExists(atPath: parent.appendingPathComponent(name).path) }

    /// The folder most projects live in, or the home folder.
    private var defaultParent: String {
        let parents = store.projects.map { URL(fileURLWithPath: $0.dir).deletingLastPathComponent().path }
        let counts = Dictionary(grouping: parents, by: { $0 }).mapValues(\.count)
        return counts.max { $0.value < $1.value }?.key ?? NSHomeDirectory()
    }

    var body: some View {
        Form {
            Section("Projeto") {
                TextField("Nome", text: $name, prompt: Text("loja"))
                    .disabled(running)
                if !name.isEmpty && !validName {
                    Text("Usa letras minúsculas, números e hífens.").font(.caption).foregroundStyle(.red)
                }
                if exists {
                    Text("Já existe uma pasta com este nome.").font(.caption).foregroundStyle(.red)
                }
                LabeledContent("Pasta") {
                    HStack {
                        Text(parent.appendingPathComponent(name.isEmpty ? "…" : name).path).foregroundStyle(.secondary)
                        Button("Alterar…") { chooseParent() }.disabled(running)
                    }
                }
                LabeledContent("Endereço", value: name.isEmpty ? "—" : "https://\(name).test")
            }

            Section {
                if let error = store.laravelError {
                    Text(error).foregroundStyle(.red)
                } else if store.laravelReleases.isEmpty {
                    ProgressView().controlSize(.small)
                } else {
                    Picker("Laravel", selection: $major) {
                        ForEach(store.laravelReleases) { release in
                            Text("Laravel \(release.major) (\(release.version))").tag(release.major)
                        }
                    }
                    .onChange(of: major) { _, _ in php = release?.default_php ?? "" }
                    Picker("PHP", selection: $php) {
                        ForEach(release?.compatible_php ?? [], id: \.self) { version in
                            Text("PHP \(version)").tag(version)
                        }
                    }
                }
                Picker("Base de dados", selection: $database) {
                    Text("SQLite (sem servidor)").tag(ServiceInstance?.none)
                    ForEach(store.databaseInstances) { Text($0.title).tag(Optional($0)) }
                }
                if store.settings?.cache != nil {
                    Toggle("Usar Valkey para Redis", isOn: $useCache)
                }
                Toggle("Instalar mesmo com vulnerabilidades conhecidas", isOn: $allowInsecure)
                if allowInsecure {
                    Label("O Composer recusa versões com falhas de segurança publicadas e sem correção. Usa isto só para testar uma versão antiga.", systemImage: "exclamationmark.triangle")
                        .font(.caption)
                        .foregroundStyle(.orange)
                }
            } header: {
                Text("Versões")
            } footer: {
                Text("O StackNest oferece a versão atual do Laravel e as duas anteriores. O email do projeto vai para a caixa de correio local.").font(.caption).foregroundStyle(.secondary)
            }

            Section {
                HStack {
                    if running {
                        ProgressView().controlSize(.small)
                        Text("A instalar com o Composer…")
                    } else if finished == true {
                        Label("Projeto criado", systemImage: "checkmark.circle.fill").foregroundStyle(.green)
                        Button("Abrir https://\(name).test") {
                            NSWorkspace.shared.open(URL(string: "https://\(name).test")!)
                        }
                        Button("Ver nos projetos", action: onCreated)
                    } else if finished == false {
                        Label("A instalação falhou; vê os detalhes abaixo.", systemImage: "xmark.octagon").foregroundStyle(.red)
                    }
                    Spacer()
                    Button("Criar projeto") { Task { await create() } }
                        .keyboardShortcut(.defaultAction)
                        .disabled(running || !validName || exists || release == nil || php.isEmpty)
                }
                if !output.isEmpty {
                    ScrollViewReader { proxy in
                        ScrollView {
                            Text(output)
                                .font(.caption.monospaced())
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .textSelection(.enabled)
                                .id("end")
                        }
                        .frame(minHeight: 180, maxHeight: 320)
                        .onChange(of: output) { _, _ in proxy.scrollTo("end", anchor: .bottom) }
                    }
                }
            }
        }
        .formStyle(.grouped)
        .task {
            await store.loadLaravelReleases()
            if major == 0, let first = store.laravelReleases.first {
                major = first.major
                php = first.default_php
            }
        }
    }

    private func chooseParent() {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.canCreateDirectories = true
        panel.directoryURL = parent
        panel.prompt = "Escolher"
        if panel.runModal() == .OK, let url = panel.url {
            parentPath = url.path
        }
    }

    private func create() async {
        running = true
        finished = nil
        output = ""
        var arguments = ["new", name, "--laravel", String(major), "--php", php]
        if allowInsecure { arguments.append("--allow-insecure") }
        if let database { arguments += ["--db", "\(database.engine):\(database.version)"] }
        if useCache { arguments.append("--redis") }
        let status = await CLI.stream(arguments, in: parent) { text in
            Task { @MainActor in output += text }
        }
        running = false
        finished = status == 0
    }
}

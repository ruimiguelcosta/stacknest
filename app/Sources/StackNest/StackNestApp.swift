import AppKit
import SwiftUI

@main
struct StackNestApp: App {
    @State private var store = AppStore()

    init() {
        // Launched as a plain executable during development, the app still needs a Dock icon and menus.
        NSApplication.shared.setActivationPolicy(.regular)
    }

    var body: some Scene {
        Window("StackNest", id: "main") {
            ContentView()
                .environment(store)
                .environment(\.locale, Locale(identifier: "pt_PT"))
                .frame(minWidth: 960, minHeight: 620)
                .task { store.start() }
        }
        .windowToolbarStyle(.unified)

        MenuBarExtra("StackNest", systemImage: store.daemonAlive ? "shippingbox.fill" : "shippingbox") {
            MenuBarContent()
                .environment(store)
        }
    }
}

enum AppSection: String, CaseIterable, Identifiable {
    case home, projects, services, newProject
    var id: String { rawValue }

    var title: String {
        switch self {
        case .home: return "Início"
        case .projects: return "Projetos"
        case .services: return "Serviços"
        case .newProject: return "Novo projeto Laravel"
        }
    }

    var icon: String {
        switch self {
        case .home: return "shield.lefthalf.filled"
        case .projects: return "folder"
        case .services: return "server.rack"
        case .newProject: return "plus.square"
        }
    }
}

struct ContentView: View {
    @Environment(AppStore.self) private var store
    @State private var section: AppSection? = .home
    @State private var selectedProject: ProjectState?

    var body: some View {
        @Bindable var store = store
        NavigationSplitView {
            List(selection: $section) {
                ForEach(AppSection.allCases) { item in
                    Label(item.title, systemImage: item.icon).tag(item)
                }
                if let mail = store.services.first(where: { $0.web_url != nil }), let url = mail.web_url.flatMap(URL.init) {
                    Button {
                        NSWorkspace.shared.open(url)
                    } label: {
                        Label("Caixa de correio", systemImage: "envelope")
                    }
                    .buttonStyle(.plain)
                }
            }
            .navigationSplitViewColumnWidth(min: 190, ideal: 210)
            .safeAreaInset(edge: .bottom) { DaemonBadge().padding(10) }
        } detail: {
            VStack(spacing: 0) {
                SetupBanner()
                switch section ?? .home {
                case .home:
                    HomeView(openProject: { project in
                        selectedProject = project
                        section = .projects
                    })
                case .projects:
                    ProjectsView(selection: $selectedProject)
                case .services:
                    ServicesView()
                case .newProject:
                    NewProjectView(onCreated: { section = .projects })
                }
            }
        }
        .alert("StackNest", isPresented: Binding(get: { store.alert != nil }, set: { if !$0 { store.alert = nil } })) {
            Button("OK") { store.alert = nil }
        } message: {
            Text(store.alert ?? "")
        }
        #if DEBUG
        .onAppear {
            DebugSnapshot.capture(sections: ["home", "projects", "services", "newProject"]) { name in
                section = AppSection(rawValue: name)
                if name == "projects" {
                    selectedProject = store.projects.first { $0.name == "dourovinhateiro" } ?? store.projects.first
                }
            }
        }
        #endif
    }
}

struct DaemonBadge: View {
    @Environment(AppStore.self) private var store

    var body: some View {
        HStack(spacing: 6) {
            Circle()
                .fill(store.daemonAlive ? Color.green : Color.red)
                .frame(width: 8, height: 8)
            Text(store.daemonAlive ? "A correr" : "Parado")
                .font(.caption)
                .foregroundStyle(.secondary)
            Spacer()
        }
    }
}

/// Explains what is missing before sites can open in the browser.
struct SetupBanner: View {
    @Environment(AppStore.self) private var store

    var body: some View {
        if !CLI.isInstalled || !store.daemonAlive {
            banner(
                icon: "exclamationmark.triangle.fill", color: .orange,
                text: CLI.isInstalled ? "O StackNest não está a correr." : "O serviço do StackNest ainda não está instalado.",
                action: CLI.isInstalled ? "Reiniciar" : "Instalar"
            ) {
                Task { CLI.isInstalled ? await store.restart() : await store.installEngine() }
            }
        } else if !store.systemReady {
            banner(
                icon: "lock.shield", color: .blue,
                text: "Falta ligar os domínios .test e confiar no certificado HTTPS (pede a password de administrador).",
                action: "Configurar"
            ) {
                Task { await store.configureSystem() }
            }
        } else if store.engineUpdateAvailable {
            banner(icon: "arrow.down.circle", color: .blue, text: "Esta versão da app traz um motor mais recente.", action: "Atualizar") {
                Task { await store.installEngine() }
            }
        }
    }

    private func banner(icon: String, color: Color, text: String, action: String, perform: @escaping () -> Void) -> some View {
        HStack {
            Image(systemName: icon).foregroundStyle(color)
            Text(text)
            Spacer()
            Button(action, action: perform).disabled(store.busy)
        }
        .padding(10)
        .background(color.opacity(0.1))
    }
}

struct MenuBarContent: View {
    @Environment(AppStore.self) private var store
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        Text(store.daemonAlive ? "StackNest a correr · \(store.projects.filter(\.running).count) sites" : "StackNest parado")
        Divider()
        ForEach(store.projects.filter { $0.url != nil }.prefix(20)) { project in
            Button(project.displayName) { NSWorkspace.shared.open(project.url!) }
        }
        Divider()
        if let mail = store.services.first(where: { $0.web_url != nil })?.web_url.flatMap(URL.init) {
            Button("Caixa de correio") { NSWorkspace.shared.open(mail) }
        }
        Button("Abrir StackNest") {
            openWindow(id: "main")
            NSApp.activate(ignoringOtherApps: true)
        }
        Button("Reiniciar serviços") { Task { await store.restart() } }
        Divider()
        Button("Sair da app") { NSApp.terminate(nil) }
            .keyboardShortcut("q")
    }
}

import AppKit
import SwiftUI

struct ServicesView: View {
    @Environment(AppStore.self) private var store

    var body: some View {
        Form {
            Section("Serviços") {
                ForEach(store.services) { service in
                    HStack(alignment: .top) {
                        StatusDot(ok: service.running).padding(.top, 5)
                        VStack(alignment: .leading, spacing: 2) {
                            Text(service.title).font(.headline)
                            if let error = service.error {
                                Text(error).font(.caption).foregroundStyle(.red)
                            } else if let port = service.port {
                                Text(detail(service, port: port)).font(.caption).foregroundStyle(.secondary).textSelection(.enabled)
                            }
                        }
                        Spacer()
                        if let url = service.web_url.flatMap(URL.init) {
                            Button("Abrir") { NSWorkspace.shared.open(url) }
                        }
                        if let log = service.log {
                            Button("Registo") { Finder.log(log) }
                        }
                    }
                }
                if store.services.isEmpty {
                    Text("Sem informação: o StackNest não está a correr.").foregroundStyle(.secondary)
                }
            }

            Section {
                LabeledContent("Domínios .test") {
                    systemState(store.resolverConfigured)
                }
                LabeledContent("Certificado HTTPS de confiança") {
                    systemState(store.caTrusted)
                }
                if !store.systemReady {
                    Button("Configurar (pede a password de administrador)") {
                        Task { await store.configureSystem() }
                    }
                }
            } header: {
                Text("Sistema")
            } footer: {
                Text("O StackNest responde aos domínios .test na porta 5354 e emite certificados com uma autoridade local, guardada em ~/.stacknest/caddy.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
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
                    Button("Reiniciar tudo") { Task { await store.restart() } }
                        .disabled(store.busy)
                    Button("Registo do StackNest") { Finder.log(CLI.home.appendingPathComponent("logs/stacknest.log").path) }
                    Button("Abrir pasta ~/.stacknest") { Finder.open(CLI.home.path) }
                }
            }
        }
        .formStyle(.grouped)
        .task { await store.checkSystem() }
    }

    private func detail(_ service: ServiceState, port: Int) -> String {
        switch service.name {
        case "mariadb": return "127.0.0.1:\(port) · utilizador root, sem password"
        case "mailpit": return "SMTP 127.0.0.1:\(port) · MAIL_HOST=127.0.0.1 MAIL_PORT=\(port)"
        default: return "Porta \(port)"
        }
    }

    private func systemState(_ ok: Bool) -> some View {
        Label(ok ? "Configurado" : "Por configurar", systemImage: ok ? "checkmark.circle.fill" : "xmark.circle")
            .foregroundStyle(ok ? .green : .orange)
    }
}

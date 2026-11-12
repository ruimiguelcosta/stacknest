# StackNest

Ambiente de desenvolvimento PHP para macOS (Apple Silicon), projeto a projeto, gratuito e de código aberto.

- **PHP 8.1 a 8.5**: cada projeto tem a sua versão, o seu PHP-FPM e o seu OPcache. O Xdebug liga-se por projeto.
- **Bases de dados**: MariaDB, MySQL e PostgreSQL, em várias versões ao mesmo tempo e em portas diferentes. A base de dados de cada projeto é criada e o `.env` é atualizado automaticamente (com cópia de segurança).
- **Valkey** (compatível com Redis), **Typesense** (pesquisa para o Laravel Scout) e **Mailpit** (caixa de correio local em `https://mail.test`). Também é possível simular emails recebidos no formato Postmark, Mailgun ou MIME.
- **Domínios `.test` com HTTPS**: os certificados são emitidos por uma autoridade local.
- **Laravel**: cria projetos na versão atual ou numa das duas anteriores.
- **Segurança**: a página inicial mostra os CVEs mais recentes que afetam as versões instaladas em cada projeto. Usa o OSV para os pacotes do `composer.lock` e a NVD para o PHP e as bases de dados.

O StackNest não precisa do Homebrew nem de outras ferramentas instaladas. Tudo o que usa vem da Release [`runtimes`](https://github.com/ruimiguelcosta/stacknest/releases/tag/runtimes), é verificado com sha256 e fica em `~/.stacknest`.

## Instalar

1. Transfere o `.dmg` da [última versão](https://github.com/ruimiguelcosta/stacknest/releases/latest) e arrasta o StackNest para Aplicações.
2. Abre a app e escolhe as versões de PHP, a base de dados e os serviços. A app transfere-os e instala-os.
3. No fim, a app pede a password de administrador uma única vez, para ligar os domínios `.test` e confiar no certificado HTTPS.

O StackNest arranca automaticamente quando entras no Mac. Para usar o `php` e o `composer` do StackNest no terminal, acrescenta esta linha ao `~/.zshrc`:

```sh
export PATH="$HOME/.stacknest/bin:$PATH"
```

## Linha de comandos

```sh
stacknest list                                   # projetos e versões
stacknest link --php 8.4 ~/projetos/loja         # servir uma pasta em https://loja.test
stacknest new blog --laravel 12 --db mariadb     # novo projeto Laravel com base de dados
stacknest set --php 8.5 --xdebug=true            # mudar definições do projeto atual
stacknest service enable postgres --version 18   # adicionar um servidor
stacknest db create --env                        # base de dados do projeto atual + .env
stacknest cves                                   # relatório de vulnerabilidades (JSON)
stacknest import-herd                            # importar os sites do Laravel Herd
```

## Desenvolvimento

- Motor (Go): `go test ./...` e `go build ./cmd/stacknest`
- App (SwiftUI): `app/scripts/build-app.sh` gera o `dist/StackNest.app`
- Runtimes: o workflow **Publish runtimes** compila e publica o catálogo
- Versões da app: publica-se uma tag `v*`. O workflow **Release app** gera o `.dmg` e assina-o e notariza-o quando os segredos do certificado Developer ID estão configurados.

Os programas incluídos mantêm as suas licenças originais: PHP License, GPL (MariaDB, MySQL e Typesense), PostgreSQL License, BSD (Valkey) e MIT (Mailpit e Caddy).

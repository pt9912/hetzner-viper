# Entwickeln an viper

Diese Anleitung richtet sich an Maintainer und beschreibt, wie Sie `viper` bauen und prüfen. Entwickelt wird im Devcontainer, in dem Go und die übrigen Werkzeuge installiert sind.

## Entwickeln im Devcontainer

Entwickelt wird nur im Devcontainer. Er enthält Go, golangci-lint, `make`, `git`, `gh`, `jq`, `shellcheck`, `tmux` und Docker. Alle `make`-Ziele (`fmt`, `vet`, `lint`, `test`, `build`, `dist`) laufen direkt mit diesen Werkzeugen. Das `Dockerfile` enthält nur noch die Release-Stages (`make dist-docker` baut damit ohne Go auf dem Rechner) und die Pins. `make help` zeigt alle Ziele.

## Der Devcontainer

`u-boot` erzeugt den Devcontainer aus der Projektdatei `u-boot.yaml` (Sandbox-Profil mit verschachteltem Docker). Die erzeugten Dateien liegen in `.devcontainer/` (`Dockerfile`, `devcontainer.json`, `docker-init.sh`, `devcontainer.env.example`). `devcontainer.env` ist Ihre eigene Datei und steht in `.gitignore`. Die Werkzeug-Versionen stehen als `ARG` im erzeugten `Dockerfile`; ändern Sie sie nie dort, sondern in `u-boot.yaml` (siehe „Einen Pin heben“). Nach jeder Änderung an `u-boot.yaml`:

```bash
u-boot generate devcontainer
```

Der Devcontainer wird mit `make` bedient (`tools/mk/devcontainer.mk`, braucht das Programm `u-boot` auf dem Host; fehlt es, endet das Ziel mit Exit-Code 2):

| Ziel | Wirkung | Optionen |
|---|---|---|
| `make dev-up` | startet den Devcontainer | `RECREATE=1` legt ihn neu an, `INSTANCE=<name>` wählt eine Instanz |
| `make dev-shell` | startet ihn wie `dev-up` und öffnet eine Shell darin | `INSTANCE=<name>` |
| `make dev-down` | stoppt ihn; er bleibt bestehen | `INSTANCE=<name>` |
| `make dev-remove` | entfernt ihn **samt Volumes ohne Rückfrage** | `INSTANCE=<name>` |

`dev-up` und `dev-shell` reichen `GIT_TOKEN` und `GH_TOKEN` aus `gh auth token` an den Container weiter, wenn sie nicht gesetzt sind. Ein gesetzter Wert hat Vorrang.

Was Sie beachten müssen:

* **Der Arbeitsbaum ist nicht gemountet.** Der Workspace ist ein Volume mit einem `git clone` von origin. Änderungen in Ihrem Arbeitsbaum auf dem Host sehen Sie im Container erst, nachdem Sie sie gepusht und im Container geholt haben, und umgekehrt.
* **Es werden Volumes angelegt:** der Workspace-Clone und das verschachtelte Docker (`/var/lib/docker`). `make dev-remove` löscht sie. Nicht gepushte Arbeit im Container geht damit verloren.
* **Der Container läuft mit `--privileged`,** damit das verschachtelte Docker startet. Er ist dann nicht mehr vom Host isoliert; `u-boot generate devcontainer` warnt davor. Starten Sie ihn nur mit Repositories, denen Sie vertrauen.
* **`sshd` fehlt im Container,** weil das Feature einen Login-Dienst mit Root-Login öffnen würde. Die Tests mit einem echten `sshd` werden übersprungen; `tool-pruefung` meldet das als Warnung.
* **Das Image installiert Werkzeuge beim Bau aus dem Netz.** Go und golangci-lint prüft das Dockerfile über die SHA-256-Summe; die Homebrew-Formeln (`ripgrep`, `jq`, `shellcheck`, `tmux`) sind nicht gepinnt, ein späterer Bau kann also andere Versionen enthalten.

## Die Gates

`make gates` führt alle Prüfungen nacheinander aus; `make check` ist ein Alias dafür. Der Lauf `tests` der CI ruft `make check` auf. Die Kette steht in der Variablen `GATE_CHECKS` im `Makefile`. Die Fragmente unter `tools/mk/` tragen sich dort ein (`GATE_CHECKS := <ziel> $(GATE_CHECKS)`), bevor `gates` definiert wird. Bei einem seriellen Lauf gilt diese Reihenfolge; unter `make -j` ist sie nicht zugesagt.

| Gate | Prüft |
|---|---|
| `tool-pruefung` | dass die Werkzeuge des laufenden Kontexts die gepinnten Versionen tragen (siehe unten) |
| `pinsync` | dass die Pins an allen ihren Stellen gleich sind (siehe unten) |
| `fmt` | gofmt |
| `vet` | `go vet` |
| `lint` | golangci-lint mit `.golangci.yml` |
| `test` | `go test -race ./...` |

Lassen Sie vor jedem Push die ganze Kette laufen, nicht nur `make test`: Schon eine Lint-Meldung (etwa zu hohe Komplexität) macht den Lauf auf `main` rot.

### Pins

Zwei Werkzeuge sind gepinnt:

| Pin | Stellen |
|---|---|
| Go (`1.27.3`) | `go.mod` (Direktive `go`), `Dockerfile` (`ARG GO_VERSION`), `.devcontainer/Dockerfile` (`ARG GO_VERSION`) |
| golangci-lint (`v2.15.0`) | `Dockerfile` (`ARG GOLANGCI_LINT_VERSION`), `.devcontainer/Dockerfile` (`ARG GOLANGCI_LINT_VERSION`) |

* **`pinsync`** (`tools/mk/pinsync.mk`) vergleicht die Werte an diesen Stellen mit `d-check` (Modul `pinsync`, die Stellen stehen in `.d-check.yml`). Es prüft Gleichheit, keine Aktualität. Das Image von `d-check` ist über seinen Digest gepinnt und läuft ohne Netz; der erste Lauf braucht Zugriff auf `ghcr.io`, um es zu ziehen.
* **`tool-pruefung`** (`tools/mk/tool-pruefung.mk`, Skript `tools/tool-pruefung.sh`) prüft die Werkzeuge selbst gegen die Pins in `go.mod` und im `Dockerfile`. Es prüft die installierten Werkzeuge: Go und golangci-lint auf die Version, dazu `bash`, `make`, `git`, `docker`, `gh`, `curl`, `jq`, `shellcheck`, `ssh-keygen` und `tmux` auf Anwesenheit. Das Skript prüft vorab, dass `go.mod` und `Dockerfile` dieselbe Go-Version nennen. Ohne `sshd` warnt es nur. Von Hand: `bash tools/tool-pruefung.sh`.

### Einen Pin heben

1. Den Wert an allen Stellen der Tabelle ändern. Für den Devcontainer ändern Sie `u-boot.yaml` (bei golangci-lint `u-boot config set devcontainer.features.golangci-lint.version v2.15.0`) und erzeugen `.devcontainer/` mit `u-boot generate devcontainer` neu. Bei Go liefert der Katalog von `u-boot` den Standardwert; weicht er von `go.mod` ab, setzen Sie `devcontainer.features.go-toolchain.version`.
2. `make gates` laufen lassen. `pinsync` wird rot, solange eine Stelle fehlt; `tool-pruefung` wird rot, wenn ein Image oder der Devcontainer noch die alte Version trägt (bauen Sie ihn neu: `make dev-up RECREATE=1`).
3. Den Grund der Hebung in den Commit-Text schreiben.

## Bauen

* `make build` baut `viper` für Ihren Rechner nach `dist/viper` (`make build VERSION=v0.5.0 GOOS=darwin GOARCH=arm64` für ein anderes Ziel, `GOOS=windows GOARCH=amd64` für `dist/viper.exe`). Geprüft wird mit `./dist/viper version`.
* `make dist VERSION=v0.5.0` baut alle Release-Dateien samt `SHA256SUMS` nach `dist/`, genau wie der Workflow (siehe [`releasing.md`](releasing.md)).

Beide laufen in Docker, auch im Devcontainer, damit der Weg der Releases überall derselbe ist.

### Tests des Objektspeichers

* `internal/natsmcp/bucket_test.go` startet einen echten `nats-server` mit JetStream (als Bibliothek) und den Rechten, die `viper` schreibt (`natsserver.AuthorizationBlock(…, true)`): Ablegen und Holen, jede Verweigerung, der Bucket, ein 3-MiB-Objekt (**Flusskontrolle**: ohne `$JS.FC…` hängt das Lesen; kleine Objekte merken es nicht), der Rollup-Kopf. `attachments_test.go` hat die Prüfungen von Art, Namen und ZIP (auch eine Bombe mit gefälschtem Verzeichnis) und die Wege der Werkzeuge und Befehle durch einen echten Server.
* Unter Windows: keine Pfade mit `\` in der Konfiguration von `nats-server` (`filepath.ToSlash`), kein `*` in Dateinamen, und die Meldung einer fehlenden Datei ist eine andere (nur den Namen der Datei prüfen).
* Läufe gegen echte Maschinen sind nicht Teil von `make check`; die Läufe vom 14.11.2026 (Compose, k3d, Hetzner) stehen in `docs/plan/nats-objekte.md` §13. Wer sie wiederholt: eigene Namen (`ctest`, `ktest`, `vtest`), danach `viper down --group` und nur eigene Schlüssel und Firewalls löschen.

### Das Image der Maschinen

Das Image `viper-machine` (`image/`) ist das der Anbieter `kubernetes` und `compose`.

* `make image` baut es (`viper-machine:<Version>`), `make image-test` prüft es mit `image/test.sh` (48 Prüfungen: `sshd`, Host-Zertifikat, Werkzeuge, ein Volume wie im Cluster, NATS). Der Benutzer baut es mit `viper image build` aus den Dateien im Binary.
* **Standard für `K8S_IMAGE` und `COMPOSE_IMAGE`:** das veröffentlichte Image der Version von `viper` (`model.DefaultMachineImage`, `ghcr.io/pt9912/viper-machine:<Version>`). Das gibt es nur für eine Release-Version (`vX.Y.Z` ohne Zusatz). Ein Build aus der Entwicklung (`make build` ohne `VERSION=vX.Y.Z`, `dev`, `v0.18.0-3-gabc`, `-dirty`) hat kein veröffentlichtes Image: Dort ist der Name Pflicht, und `viper image build` baut das Image aus den Dateien im Binary. Den Standard eines Release probieren Sie mit einem lokalen Build: `go build -ldflags "-X main.version=v0.18.0" ./cmd/viper`.
* **Veröffentlichen:** Der Workflow `machine image` (`.github/workflows/image.yml`) baut und testet das Image bei jedem Tag `vX.Y.Z` je Architektur (amd64 auf `ubuntu-latest`, arm64 auf `ubuntu-24.04-arm`, ohne Emulation) und veröffentlicht es als ein Image mit beiden Architekturen: `ghcr.io/pt9912/viper-machine:<Version>` und `:latest`. Das Paket ist öffentlich.
* **Ein Image zu einem vorhandenen Tag nachholen** (ein Tag wird nie verschoben): `gh workflow run "machine image" -f tag=vX.Y.Z -f ref=main` baut es mit den Dateien von `main`. Der Lauf bricht ab, wenn sich eine Datei, die ins Image kommt (`image/Dockerfile`, `cloud-init.yaml`, `image/seed.py`, `image/sshd_config`, `image/entry.sh`, `image/nats-supervisor.sh`), zwischen Tag und `ref` unterscheidet; das Image ist dann das, was das Tag beschreibt.
* Der Test läuft auf den Runnern als anderer Benutzer als lokal (uid 1001, lokal oft 1000): Was er auf einem gemounteten Verzeichnis anlegt, muss `builder` (uid 1000) lesen und löschen können, und das Aufräumen am Ende gibt die Dateien dem aufrufenden Benutzer zurück.

### Die Oberfläche (`viper ui`)

`internal/adapter/driving/web` ist ein treibender Adapter: `net/http`, die Seiten in `web/static` (HTML, CSS, JavaScript, eingebettet mit `//go:embed`, kein Build-Schritt). Er importiert die CLI nicht; `cli/ui.go` gibt ihm ein `web.Backend` (Liste der Rechner aus den Einstellungsdateien, `runCheck`, `showLog` im selben Prozess). Die Regeln stehen in `docs/plan/ui.md` §3; `web_test.go` prüft jede davon (Host, einmaliger Schlüssel, Cookie, `Origin`, CSP, Ausgaben ohne geheime Werte) und durchsucht die Seiten nach `innerHTML`, `eval`, Inline-Stilen und fremden Adressen. Neue Seiten oder Anfragen dürfen keine dieser Regeln aufweichen: keine GET-Anfrage mit Nebenwirkung, jede Ausgabe durch `show` (reinigen, schwärzen), alle Texte mit `textContent`. Aktionen laufen als Prozess von `viper` selbst (`cli/ui_proc*.go`, unter Windows mit eigener Prozessgruppe und `CTRL_BREAK`, dort noch ungeprüft); `web/runs.go` hält die Läufe (ein Lauf je Rechner, Ausgabe in Stücken, eine offene Zeile nach 600 ms ist eine Frage, Abbrechen = Interrupt, nach 20 s Kill). NATS-Aktionen (`web/natsrun.go`) bauen aus geprüften Feldern eine Befehlszeile (`--name=wert`, der Text über eine Datei des Laufs, eine Datei zum Senden über `POST /api/upload` als `application/octet-stream` in ein privates Verzeichnis, ein geholter Datei über `GET /api/download` nur als `octet-stream` und `attachment`). Die Texte der Seiten stehen in `static/i18n.js` (Englisch und Deutsch; ein Test vergleicht die Schlüssel mit den Seiten und untereinander); die Einstellungsseite (`cli/uisettings.go`) zeigt Werte nur für Namen einer Positivliste. Die Tests in `runs_test.go` und `nats_test.go` benutzen einen Prozess, den der Test steuert.

### Browser-Tests der Oberfläche (`e2e/`)

`e2e/` enthält Playwright-Tests (Chromium) für `viper ui`; sie gehören nicht zum Programm und nicht zu den Gates. Sie starten das echte `viper ui` in einem Wegwerf-Verzeichnis mit `PROVIDER=compose` und einem gefälschten `docker` (es gibt nur eine Zeile aus und schläft), sodass nirgends etwas angelegt wird. Je Test läuft ein eigener Server (der Schlüssel gilt nur einmal).

```bash
cd e2e && npm install && npx playwright install chromium   # einmal
npx playwright test                                         # baut viper selbst (oder VIPER_BIN=…)
```

Auf einer Maschine, die `viper` für dieses Repository anlegt, richtet `.viper.sh` Node, Playwright und Chromium mit `VIPER_E2E=1` ein (sonst nicht; der Schritt braucht `sudo` für die Systembibliotheken).

In einem Container ohne die Systembibliotheken von Chromium (`npx playwright install-deps`, braucht root) hilft `E2E_CONTAINER=1` (`--no-sandbox`, kein `/dev/shm`); ohne installierte Schriften stürzt Chromium beim ersten Text ab. Die Tests prüfen, was nur ein Browser zeigt: Cookie und Weiterleitung (der Start über die `file://`-Startdatei), CSP ohne Verstöße, `localhost` abgelehnt, eine Seite eines fremden Ursprungs, die nichts starten kann, Ausgaben als Text (kein Markup), Rückfrage mit Eingabefeld, Abbrechen und `--readonly`, Zugänglichkeit (axe-core: WCAG 2.1 AA in Hell und Dunkel, Englisch und Deutsch), Tastaturbedienung, Sprachwahl und die Seite „Settings“.

## Tests

Die Tests beschreibt [`architektur.md`](architektur.md#tests). Der Devcontainer enthält `bash`, `git`, `jq`, `ssh-keygen` und `shellcheck`; ein fehlendes Werkzeug lässt den Test fehlschlagen (`make test` setzt `VIPER_REQUIRE_TOOLS=1`), sonst überspringt er ihn.

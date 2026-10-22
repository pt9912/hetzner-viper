# Hetzner Viper

[English](README.md) | **Deutsch**

**VIPER** steht für **VI**rtual **PER**son: ein Code-Agent, der wie eine weitere Person im Team auf einem eigenen Rechner in Ihrem Repository arbeitet.

## Was ist Hetzner Viper?

Eine Sammlung von Bash-Skripten, die mit einem Befehl einen entfernten Linux-Rechner bei Hetzner Cloud einrichten. Auf diesem Rechner arbeitet der Code-Agent [Claude Code](https://github.com/anthropics/claude-code) remote in Ihrem Repository: Er liest und ändert Code, führt Builds und Tests aus und committet. Pro Git-Repository gibt es einen eigenen Rechner. Das Repository wird beim Einrichten automatisch geklont. Sie steuern den Agenten per SSH, VS Code Remote-SSH oder vom iPad, in einer tmux-Sitzung, die Verbindungsabbrüche übersteht. Wer fertig ist, löscht den Rechner wieder, und es entstehen keine laufenden Kosten.

Zielgruppe sind Entwicklerinnen und Entwickler mit eigenem Hetzner- und Cloudflare-Konto.

## Was kann ich heute tun?

Diese Befehle sind vorhanden. Stand der Prüfung: Offline-Tests gegen Mocks (dazu ein echter sshd) und Läufe auf echtem Hetzner Cloud (cpx02, Ubuntu 26.04) mit Cloudflare-DNS: Setup, Preflight, Anlegen, Wiederverwenden und Neuanlegen eines Rechners samt Neustart nach Updates, Check, SSH, Löschen samt Schutz vor ungesicherter Arbeit, Claude über das Abo-Token und über Ollamas Cloud mit Tool-Nutzung. **Noch nicht echt gelaufen:** `VIPER_GH_TOKEN`/gh, Deploy-Keys, das iPad, ein vLLM-Server (siehe [Grenzen](#sicherheit-und-grenzen)).

Alles läuft per `make` in einem Docker-Container. Lokal brauchen Sie nur Docker und `make` (`ssh`, `hcloud`, `jq` usw. kommen aus dem [`Dockerfile`](Dockerfile)):

```bash
cp .env.local.example .env.local   # DNS_DOMAIN und Tokens eintragen, dann: chmod 600 .env.local
make                                                # alle Targets anzeigen (make help)
make setup                                          # Login-Key und lokale Host-CA anlegen
make preflight MACHINE=web                          # nur lesende Prüfung: Einstellungen, Hetzner, Cloudflare, GitHub
make up MACHINE=web REPO=https://example.org/org/web.git   # Rechner "web" einrichten, Repo klonen
make check MACHINE=web                              # nur lesend: DNS, Zugriff über den Namen wie ein anderes Gerät, Server-Zustand
make ssh MACHINE=web                                # Shell auf dem Rechner (CMD="..." für einen Befehl)
make down MACHINE=web                               # Rechner "web" löschen
make log [MACHINE=web] [FULL=1]                     # Ereignisse der Rechner oder die vollständige Ausgabe eines Rechners
```

Ohne Container rufen Sie die Skripte direkt auf: `./setup.sh`, `./preflight.sh web`, `./start.sh web <url>`, `./check.sh web`, `./connect.sh web`, `./stop.sh web`.

- `make preflight MACHINE=<name>` (oder `./preflight.sh <name> [repo-url]`) prüft vorab nur lesend, bevor etwas angelegt wird: Einstellungen, lokales Setup, Hetzner-Token, Bestellbarkeit des Servertyps am Standort, Image, Cloudflare-Zone und -Eintrag, GitHub-Token und Repo-Zugriff. `start.sh` führt es zuerst aus.
- `make test` führt die Tests gegen Mocks aus (offline, ohne Kosten), `make docker-test` dieselben im Container ohne Netz.
- Mehrere Rechner parallel: `make up MACHINE=api …` neben `web`. Jeder hat eigenen Server und DNS-Namen `<SERVER_PREFIX>-<name>.<DNS_DOMAIN>` (Standard-Präfix `claude`).
- Mit `VIPER_GH_TOKEN` (GitHub-PAT, am besten fein granular pro Rechner mit Contents und Pull requests: Read and write; ein für das lokale `gh` exportiertes `GH_TOKEN` wird bewusst nicht verwendet) ist auf dem Server die GitHub-CLI `gh` angemeldet, und `git` pusht über HTTPS. Der Agent kann so auch Pull Requests anlegen.
- Mit `VIPER_CLAUDE_TOKEN` (langlebiges Token Ihres Claude-Abos aus `claude setup-token`) ist Claude Code auf dem Server angemeldet, ohne `/login` nach jedem neuen Server.
- Oder ein anderes Modell statt der Anthropic-Modelle: `VIPER_ANTHROPIC_BASE_URL`, `VIPER_ANTHROPIC_AUTH_TOKEN` und `VIPER_CLAUDE_MODEL` richten Claude Code auf dem Server auf eine Anthropic-kompatible Schnittstelle, etwa Ollamas Cloud (`https://ollama.com`) oder einen eigenen vLLM-Server; der Preflight testet Schlüssel und Modell mit einer kleinen Anfrage.
- Docker (Engine, Buildx, Compose) kommt aus Dockers eigenem apt-Repository und ist damit aktuell. Commits auf dem Server bekommen einen Autor aus `VIPER_GIT_USER_NAME` / `VIPER_GIT_USER_EMAIL`, oder mit `VIPER_GH_TOKEN` vom GitHub-Konto (Name und noreply-Adresse).
- Optional: `VIPER_CODE_SERVER=1` (mit `VIPER_CODE_SERVER_PASSWORD`) installiert `code-server`, VS Code im Browser, das nur auf `127.0.0.1` lauscht; Zugriff über einen SSH-Tunnel (nützlich auf dem iPad).
- Homebrew (`brew`) wird auf jedem neuen Server installiert und ist in allen Shells verfügbar; `VIPER_HOMEBREW=0` lässt es weg. Werkzeuge kommen aus `VIPER_BREW_TAPS`, `VIPER_BREW_TRUST` und `VIPER_BREW_PACKAGES` (Tap, Trust, Installation, in dieser Reihenfolge) oder, mit `VIPER_BREWFILE=1`, aus einer `Brewfile` im Repository.
- `stop.sh` prüft den Git-Stand aller Repositories und Worktrees im Arbeitsverzeichnis, im Home- und in den Temp-Verzeichnissen (uncommittete Änderungen, Commits auf keinem Remote auf allen Branches, Stash-Einträge), meldet Einträge auf oberster Ebene des Arbeitsverzeichnisses, die zu keinem Repository gehören (Dateien außerhalb von Repositories an anderen Orten prüft es nicht), listet ignorierte Dateien auf und fragt bei ungesicherter Arbeit zusätzlich nach `DISCARD`.

Die vollständige Anleitung mit allen Aufgaben, Einstellungen und der Fehlerbehebung steht im [Benutzerhandbuch](docs/user/handbuch.md). Für den ersten echten Lauf beginnen Sie dort mit dem Abschnitt „Erster Testlauf mit einem kleinen Server“ (Servertyp `cpx02`, wenige Cent pro Stunde).

## Warum Hetzner Viper?

Ein Code-Agent wie Claude Code soll auf einem Rechner arbeiten, der nicht der eigene Laptop ist: mit viel Leistung, isoliert von den eigenen Daten und Zugängen, dauerhaft erreichbar per tmux und vom iPad bedienbar. Ein Dauerbetrieb lohnt sich dafür nicht. Von Hand sind Server, Firewall, DNS, SSH-Härtung und Repository-Setup bei jedem Start mühsam und fehleranfällig. Die Skripte machen daraus einen Befehl zum Starten und einen zum Löschen.

## Kerngedanke

Rechner sind Wegwerfware: Alles, was zählt, liegt im Git-Repository. Deshalb gibt es bewusst keinen persistenten Datenträger, und `stop.sh` schützt vor dem einzigen Risiko, das daraus folgt, nämlich vor ungepushter Arbeit.

## Was macht es vertrauenswürdig?

- **Tests:** `make test` (Syntaxprüfung, `shellcheck` falls installiert, Mock-Tests in [`tests/`](tests/)). Sie decken Einrichtung, Bereitschaftsprüfung, Mehrrechner-Betrieb, Git-Clone, Firewall- und DNS-Schutz sowie die Sicherheitsabfragen von `stop.sh` ab. Die erzeugte cloud-init-Konfiguration wurde einmalig mit `cloud-init schema` (cloud-init 26.2, Ubuntu 26.04 und 24.04) geprüft, dazu Paketverfügbarkeit, `sshd`-Härtung und `gh`-Flags unter 26.04 sowie die `hcloud`-Aufrufe gegen die Hilfe der CLI v1.70.1.
- **Schutz fremder Ressourcen:** `dns.sh` ändert und löscht nur DNS-Einträge, die es selbst angelegt hat.
- **Handbuch:** [`docs/user/handbuch.md`](docs/user/handbuch.md) mit Versions- und Änderungshistorie.
- **Lizenz:** MIT, siehe [`LICENSE`](LICENSE).

## Sicherheit und Grenzen

- SSH nur mit Public-Key (kein Passwort, kein Root-Login). Standardmäßig ist Port 22 aus dem ganzen Internet offen. Schränken Sie `SSH_ALLOWED_CIDRS` ein, wenn Sie nur von festen Netzen arbeiten.
- Der Benutzer `builder` hat sudo ohne Passwort und Docker-Zugriff, das entspricht Root. Nutzen Sie nur vertrauenswürdige Repositories.
- Serverseitig wird ein CA-signiertes SSH-Host-Zertifikat verwendet (sieben Tage gültig, keine automatische Erneuerung). Der private Host-Key wird dafür in den Hetzner-User-Data übertragen und ist dort für Administratoren des Projekts und für Prozesse auf dem Server lesbar. Der CA-Schlüssel bleibt lokal. Der einrichtende Rechner hinterlegt den Host-Schlüssel jedes Servers und vertraut nur diesem. Ein abgeflossener Schlüssel eines früheren Servers wird so abgelehnt, und die Verbindung funktioniert auch nach Ablauf des Zertifikats.
- Der Login-Schlüssel hat keine Passphrase.
- **Alles auf dem Server geht beim Löschen verloren**, auch eine Claude-Anmeldung per `/login` (mit `VIPER_CLAUDE_TOKEN` ist jeder neue Server automatisch wieder angemeldet).
- Die SSH-Konfiguration jedes Rechners leitet Ihren ssh-agent und X11 nie an den Server weiter, auch wenn Ihre eigene `~/.ssh/config` das tut.
- Die Skripte legen kostenpflichtige Ressourcen an. Legen Sie API-Tokens nie in Git ab.
- Teile sind noch nicht gegen echte Dienste gelaufen (siehe oben). Nutzen Sie ein eigenes Hetzner-Projekt und probieren Sie neue Einstellungen zuerst mit `make preflight` und einem kleinen Server aus.

## Voraussetzungen

Nur unter Linux erprobt. Docker und `make` (empfohlen, auch für macOS), **oder** lokal `bash`, `curl`, `jq`, `python3`, OpenSSH-Client und die `hcloud`-CLI. VS Code Remote-SSH braucht einen lokalen OpenSSH-Client; ohne ihn nutzen Sie `make ssh`. Dazu ein Hetzner-Cloud-API-Token (Lesen und Schreiben) und ein Cloudflare-Token mit **Zone DNS Edit/Read** samt Zone-ID.

## Lizenz

MIT, siehe [`LICENSE`](LICENSE).

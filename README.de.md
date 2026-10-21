# Hetzner Viper

[English](README.md) | **Deutsch**

**VIPER** steht für **VI**rtual **PER**son: ein Code-Agent, der wie eine weitere Person im Team auf einem eigenen Rechner in Ihrem Repository arbeitet.

## Was ist Hetzner Viper?

Eine Sammlung von Bash-Skripten, die mit einem Befehl einen entfernten Linux-Rechner bei Hetzner Cloud einrichten. Auf diesem Rechner arbeitet der Code-Agent [Claude Code](https://github.com/anthropics/claude-code) remote in Ihrem Repository: Er liest und ändert Code, führt Builds und Tests aus und committet. Pro Git-Repository gibt es einen eigenen Rechner. Das Repository wird beim Einrichten automatisch geklont. Sie steuern den Agenten per SSH, VS Code Remote-SSH oder vom iPad, in einer tmux-Sitzung, die Verbindungsabbrüche übersteht. Wer fertig ist, löscht den Rechner wieder, und es entstehen keine laufenden Kosten.

Zielgruppe sind Entwicklerinnen und Entwickler mit eigenem Hetzner- und Cloudflare-Konto.

## Was kann ich heute tun?

Diese Befehle sind vorhanden. Stand der Prüfung: Die Skripte sind gegen simulierte Hetzner- und Cloudflare-APIs getestet, **nicht** gegen echte Konten (siehe [Grenzen](#sicherheit-und-grenzen)).

```bash
cp .env.local.example .env.local   # DNS_DOMAIN eintragen
./setup.sh                         # Login-Key und lokale Host-CA anlegen
export HCLOUD_TOKEN=... CLOUDFLARE_API_TOKEN=... CLOUDFLARE_ZONE_ID=...
./start.sh web https://example.org/org/web.git   # Rechner "web" einrichten, Repo klonen
./stop.sh web                                    # Rechner "web" löschen
```

- `make test` führt die Tests gegen Mocks aus (offline, ohne Kosten), `make docker-test` dieselben im Container ohne Netz.
- `make up MACHINE=web REPO=<url>` und `make down MACHINE=web` führen die Skripte in einem Docker-Container aus.
- Mehrere Rechner parallel: `./start.sh api …` neben `web`. Jeder hat eigenen Server und DNS-Namen `<SERVER_PREFIX>-<name>.<DNS_DOMAIN>` (Standard-Präfix `claude`).
- Mit `GH_TOKEN` (GitHub-PAT, am besten fein granular pro Rechner) ist auf dem Server die GitHub-CLI `gh` angemeldet, und `git` pusht über HTTPS. Der Agent kann so auch Pull Requests anlegen.
- `stop.sh` prüft den Git-Stand aller Repositories und Worktrees im Arbeitsverzeichnis, im Home- und in den Temp-Verzeichnissen (uncommittete Änderungen, Commits auf keinem Remote auf allen Branches, Stash-Einträge), meldet Dateien außerhalb von Repositories, listet ignorierte Dateien auf und fragt bei ungesicherter Arbeit zusätzlich nach `DISCARD`.

Die vollständige Anleitung mit allen Aufgaben, Einstellungen und der Fehlerbehebung steht im [Benutzerhandbuch](docs/user/handbuch.md).

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
- **Alles auf dem Server geht beim Löschen verloren**, auch die Claude-Anmeldung.
- Die Skripte legen kostenpflichtige Ressourcen an. Legen Sie API-Tokens nie in Git ab.
- Nicht gegen ein echtes Hetzner- oder Cloudflare-Konto getestet. Probieren Sie sie zuerst in einem Wegwerf-Projekt aus.

## Voraussetzungen

Nur unter Linux erprobt. Docker und `make` (auch für macOS), **oder** lokal `bash`, `curl`, `jq`, `python3`, OpenSSH-Client und die `hcloud`-CLI. Dazu ein Hetzner-Cloud-API-Token (Lesen und Schreiben) und ein Cloudflare-Token mit **Zone DNS Edit/Read** samt Zone-ID.

## Lizenz

MIT, siehe [`LICENSE`](LICENSE).

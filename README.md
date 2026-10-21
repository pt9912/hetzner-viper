# Hetzner Claude Worker

## Was ist Hetzner Claude Worker?

Eine Sammlung von Bash-Skripten, die mit einem Befehl einen entfernten Linux-Rechner bei Hetzner Cloud einrichten, auf dem [Claude Code](https://claude.com/claude-code) läuft. Pro Git-Repository gibt es einen eigenen Rechner. Das Repository wird beim Einrichten automatisch geklont. Gearbeitet wird per SSH, VS Code Remote-SSH oder vom iPad. Wer fertig ist, löscht den Rechner wieder, und es entstehen keine laufenden Kosten.

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

- `make test` führt 22 Tests gegen Mocks aus (offline, ohne Kosten), `make docker-test` dieselben im Container ohne Netz.
- `make up MACHINE=web REPO=<url>` und `make down MACHINE=web` führen die Skripte in einem Docker-Container aus.
- Mehrere Rechner parallel: `./start.sh api …` neben `web`. Jeder hat eigenen Server und DNS-Namen `<name>.<DNS_DOMAIN>`.
- `stop.sh` prüft den Git-Stand und fragt bei ungepushter Arbeit zusätzlich nach `DISCARD`.

Die vollständige Anleitung mit allen Aufgaben, Einstellungen und der Fehlerbehebung steht im [Benutzerhandbuch](docs/user/handbuch.md).

## Warum Hetzner Claude Worker?

Claude Code soll auf einem Rechner laufen, der nicht der eigene Laptop ist: mit viel Leistung, dauerhaft erreichbar per tmux und vom iPad bedienbar. Ein Dauerbetrieb lohnt sich dafür nicht. Von Hand sind Server, Firewall, DNS, SSH-Härtung und Repository-Setup bei jedem Start mühsam und fehleranfällig. Die Skripte machen daraus einen Befehl zum Starten und einen zum Löschen.

## Kerngedanke

Rechner sind Wegwerfware: Alles, was zählt, liegt im Git-Repository. Deshalb gibt es bewusst keinen persistenten Datenträger, und `stop.sh` schützt vor dem einzigen Risiko, das daraus folgt, nämlich vor ungepushter Arbeit.

## Was macht es vertrauenswürdig?

- **Tests:** `make test` (Syntaxprüfung, `shellcheck` falls installiert, 22 Mock-Tests in [`tests/`](tests/)). Sie decken Einrichtung, Mehrrechner-Betrieb, Git-Clone, DNS-Schutz und die Sicherheitsabfragen von `stop.sh` ab.
- **Schutz fremder Ressourcen:** `dns.sh` ändert und löscht nur DNS-Einträge, die es selbst angelegt hat.
- **Handbuch:** [`docs/user/handbuch.md`](docs/user/handbuch.md) mit Versions- und Änderungshistorie.
- **Lizenz:** MIT, siehe [`LICENSE`](LICENSE).

## Sicherheit und Grenzen

- SSH nur mit Public-Key (kein Passwort, kein Root-Login). Standardmäßig ist Port 22 aus dem ganzen Internet offen. Schränken Sie `SSH_ALLOWED_CIDRS` ein, wenn Sie nur von festen Netzen arbeiten.
- Der Benutzer `builder` hat sudo ohne Passwort und Docker-Zugriff, das entspricht Root. Nutzen Sie nur vertrauenswürdige Repositories.
- Serverseitig wird ein CA-signiertes SSH-Host-Zertifikat verwendet (sieben Tage gültig, keine automatische Erneuerung). Der private Host-Key wird dafür in den Hetzner-User-Data übertragen und ist dort für Administratoren des Projekts und für Prozesse auf dem Server lesbar. Der CA-Schlüssel bleibt lokal.
- Der Login-Schlüssel hat keine Passphrase.
- **Alles auf dem Server geht beim Löschen verloren**, auch die Claude-Anmeldung.
- Die Skripte legen kostenpflichtige Ressourcen an. Legen Sie API-Tokens nie in Git ab.
- Nicht gegen ein echtes Hetzner- oder Cloudflare-Konto getestet. Probieren Sie sie zuerst in einem Wegwerf-Projekt aus.

## Voraussetzungen

Docker und `make`, **oder** lokal `bash`, `curl`, `jq`, `python3`, OpenSSH-Client und die `hcloud`-CLI. Dazu ein Hetzner-Cloud-API-Token (Lesen und Schreiben) und ein Cloudflare-Token mit **Zone DNS Edit/Read** samt Zone-ID.

## Lizenz

MIT, siehe [`LICENSE`](LICENSE).

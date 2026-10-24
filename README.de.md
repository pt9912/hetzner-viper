# Hetzner Viper

[English](README.md) | **Deutsch**

**VIPER** steht für **VI**rtual **PER**son: ein Code-Agent, der wie eine weitere Person im Team auf einem eigenen Rechner in Ihrem Repository arbeitet.

## Was ist Hetzner Viper?

Ein einzelnes Programm, `viper`, das mit einem Befehl einen entfernten Linux-Rechner bei Hetzner Cloud einrichtet. Auf diesem Rechner arbeitet der Code-Agent [Claude Code](https://github.com/anthropics/claude-code) remote in Ihrem Repository: Er liest und ändert Code, führt Builds und Tests aus und committet. Pro Git-Repository gibt es einen eigenen Rechner. Das Repository wird beim Einrichten automatisch geklont. Sie steuern den Agenten per SSH, VS Code Remote-SSH oder vom iPad, in einer tmux-Sitzung, die Verbindungsabbrüche übersteht. Wer fertig ist, löscht den Rechner wieder, und es entstehen keine laufenden Kosten.

Zielgruppe sind Entwicklerinnen und Entwickler mit eigenem Hetzner- und Cloudflare-Konto.

## Was kann ich heute tun?

`viper` ist eine Datei (Linux) von der [Releases-Seite](https://github.com/pt9912/hetzner-viper/releases) oder mit Homebrew: `brew install pt9912/viper/viper`. Es braucht den OpenSSH-Client (`ssh`, `ssh-keygen`, `ssh-keyscan`, `scp`) und sonst nichts: kein Docker, `make`, `hcloud` oder `jq` (`git` dient der Repository-Prüfung und `code` dem Öffnen von VS Code, beides optional).

```bash
mkdir ~/viper && cd ~/viper        # hier liegt Ihre .env.local
viper setup                        # legt .env.local aus einer Vorlage an; DNS_DOMAIN und Tokens eintragen, dann erneut:
viper setup                        # Login-Key und lokale Host-CA anlegen
viper preflight web                # nur lesende Prüfung: Einstellungen, Hetzner, Cloudflare, GitHub
viper up web --repo https://example.org/org/web.git   # Rechner "web" einrichten, Repo klonen
viper check web                    # nur lesend: DNS, Zugriff über den Namen wie ein anderes Gerät, Server-Zustand
viper ssh web                      # Shell auf dem Rechner (--cmd "..." für einen Befehl)
viper down web                     # auf ungesicherte Arbeit prüfen, dann Rechner "web" löschen
viper dns web                      # DNS-Name des Rechners "web" ausgeben
viper log [web] [--full]           # Ereignisse der Rechner oder die vollständige Ausgabe eines Rechners
```

- `viper preflight <name>` prüft vorab nur lesend, bevor etwas angelegt wird: Einstellungen, lokales Setup, Hetzner-Token, Bestellbarkeit des Servertyps am Standort, Image, Cloudflare-Zone und -Eintrag, GitHub-Token und Repo-Zugriff. `viper up` führt es zuerst aus.
- Mehrere Rechner parallel: `viper up api …` neben `web`. Jeder hat eigenen Server und DNS-Namen `<SERVER_PREFIX>-<name>.<DNS_DOMAIN>` (Standard-Präfix `claude`).
- Mit `VIPER_GH_TOKEN` (GitHub-PAT, am besten fein granular pro Rechner mit Contents und Pull requests: Read and write; ein für das lokale `gh` exportiertes `GH_TOKEN` wird bewusst nicht verwendet) ist auf dem Server die GitHub-CLI `gh` angemeldet, und `git` pusht über HTTPS. Der Agent kann so auch Pull Requests anlegen.
- Mit `VIPER_CLAUDE_TOKEN` (langlebiges Token Ihres Claude-Abos aus `claude setup-token`) ist Claude Code auf dem Server angemeldet, ohne `/login` nach jedem neuen Server.
- Oder ein anderes Modell statt der Anthropic-Modelle: `VIPER_ANTHROPIC_BASE_URL`, `VIPER_ANTHROPIC_AUTH_TOKEN` und `VIPER_CLAUDE_MODEL` richten Claude Code auf dem Server auf eine Anthropic-kompatible Schnittstelle, etwa Ollamas Cloud (`https://ollama.com`) oder einen eigenen vLLM-Server; der Preflight testet Schlüssel und Modell mit einer kleinen Anfrage.
- Docker (Engine, Buildx, Compose) kommt aus Dockers eigenem apt-Repository und ist damit aktuell. Commits auf dem Server bekommen einen Autor aus `VIPER_GIT_USER_NAME` / `VIPER_GIT_USER_EMAIL`, oder mit `VIPER_GH_TOKEN` vom GitHub-Konto (Name und noreply-Adresse).
- Optional: `VIPER_CODE_SERVER=1` (mit `VIPER_CODE_SERVER_PASSWORD`) installiert `code-server`, VS Code im Browser, das nur auf `127.0.0.1` lauscht; Zugriff über einen SSH-Tunnel (nützlich auf dem iPad).
- Optional: `VIPER_SWAP` legt eine Swap-Datei an, als Faktor vom RAM des Servers (`1` = so viel wie RAM, `0.5` = die Hälfte) oder als Größe (`4G`); standardmäßig aus. Sie fängt Speicherspitzen (`docker build`, `npm install`) ab, statt dass der OOM-Killer zuschlägt.
- Homebrew (`brew`) wird auf jedem neuen Server installiert und ist in allen Shells verfügbar; `VIPER_HOMEBREW=0` lässt es weg. Werkzeuge kommen aus `VIPER_BREW_TAPS`, `VIPER_BREW_TRUST` und `VIPER_BREW_PACKAGES` (Tap, Trust, Installation, in dieser Reihenfolge) oder, mit `VIPER_BREWFILE=1`, aus einer `Brewfile` im Repository.
- `viper down` prüft den Git-Stand aller Repositories und Worktrees im Arbeitsverzeichnis, im Home- und in den Temp-Verzeichnissen (uncommittete Änderungen, Commits auf keinem Remote auf allen Branches, Stash-Einträge), meldet Einträge auf oberster Ebene des Arbeitsverzeichnisses, die zu keinem Repository gehören (Dateien außerhalb von Repositories an anderen Orten prüft es nicht), listet ignorierte Dateien auf und fragt bei ungesicherter Arbeit zusätzlich nach `DISCARD`.

Die vollständige Anleitung mit allen Aufgaben, Einstellungen und der Fehlerbehebung steht im [Benutzerhandbuch](docs/user/handbuch.md). Für den ersten echten Lauf beginnen Sie dort mit dem Abschnitt „Erster Testlauf mit einem kleinen Server“ (Servertyp `cpx02`, wenige Cent pro Stunde).

## Warum Hetzner Viper?

Ein Code-Agent wie Claude Code soll auf einem Rechner arbeiten, der nicht der eigene Laptop ist: mit viel Leistung, isoliert von den eigenen Daten und Zugängen, dauerhaft erreichbar per tmux und vom iPad bedienbar. Ein Dauerbetrieb lohnt sich dafür nicht. Von Hand sind Server, Firewall, DNS, SSH-Härtung und Repository-Setup bei jedem Start mühsam und fehleranfällig. `viper` macht daraus einen Befehl zum Starten und einen zum Löschen.

## Kerngedanke

Rechner sind Wegwerfware: Alles, was zählt, liegt im Git-Repository. Deshalb gibt es bewusst keinen persistenten Datenträger, und `viper down` schützt vor dem einzigen Risiko, das daraus folgt, nämlich vor ungepushter Arbeit.

## Was macht es vertrauenswürdig?

- **Tests:** `make check` (gofmt, vet, golangci-lint und über 300 Tests, alles in Docker; CI bei jedem Push; `make help` zeigt die Ziele, `make build` baut `dist/viper` ohne Go-Installation). Sie decken Einrichtung, Bereitschaftsprüfung, Mehrrechner-Betrieb, Git-Clone, Firewall- und DNS-Schutz, die Sicherheitsabfragen von `viper down`, den Abbruch mit Ctrl-C und den Parser für `.env.local` (verglichen mit echtem Bash) ab. Die erzeugten cloud-init-User-Data werden mit Referenzdateien verglichen und als YAML gelesen.
- **Schutz fremder Ressourcen:** `viper` ändert und löscht nur DNS-Einträge, die es selbst angelegt hat.
- **Handbuch:** [`docs/user/handbuch.md`](docs/user/handbuch.md) mit Versions- und Änderungshistorie.
- **Releases:** Versionen stehen als Tags auf der [Releases-Seite](https://github.com/pt9912/hetzner-viper/releases) mit den `viper`-Binaries und Prüfsummen; zum Erstellen siehe [`docs/maintainer/releasing.md`](docs/maintainer/releasing.md).
- **Lizenz:** MIT, siehe [`LICENSE`](LICENSE).

## Sicherheit und Grenzen

- SSH nur mit Public-Key (kein Passwort, kein Root-Login). Standardmäßig ist Port 22 aus dem ganzen Internet offen. Schränken Sie `SSH_ALLOWED_CIDRS` ein, wenn Sie nur von festen Netzen arbeiten.
- Der Benutzer `builder` hat sudo ohne Passwort und Docker-Zugriff, das entspricht Root. Nutzen Sie nur vertrauenswürdige Repositories.
- Serverseitig wird ein CA-signiertes SSH-Host-Zertifikat verwendet (sieben Tage gültig, keine automatische Erneuerung). Der private Host-Key wird dafür in den Hetzner-User-Data übertragen und ist dort für Administratoren des Projekts und für Prozesse auf dem Server lesbar. Der CA-Schlüssel bleibt lokal. Der einrichtende Rechner hinterlegt den Host-Schlüssel jedes Servers und vertraut nur diesem. Ein abgeflossener Schlüssel eines früheren Servers wird so abgelehnt, und die Verbindung funktioniert auch nach Ablauf des Zertifikats.
- Der Login-Schlüssel hat keine Passphrase.
- **Alles auf dem Server geht beim Löschen verloren**, auch eine Claude-Anmeldung per `/login` (mit `VIPER_CLAUDE_TOKEN` ist jeder neue Server automatisch wieder angemeldet).
- Die SSH-Konfiguration jedes Rechners leitet Ihren ssh-agent und X11 nie an den Server weiter, auch wenn Ihre eigene `~/.ssh/config` das tut.
- `viper` legt kostenpflichtige Ressourcen an. Legen Sie API-Tokens nie in Git ab.
- Nutzen Sie ein eigenes Hetzner-Projekt und probieren Sie neue Einstellungen zuerst mit `viper preflight` und einem kleinen Server aus.

## Voraussetzungen

Linux, das Binary `viper` und ein OpenSSH-Client. VS Code Remote-SSH nutzt den lokalen OpenSSH-Client; ohne VS Code nutzen Sie `viper ssh`. Dazu ein Hetzner-Cloud-API-Token (Lesen und Schreiben) und ein Cloudflare-Token mit **Zone DNS Edit/Read** samt Zone-ID.

## Lizenz

MIT, siehe [`LICENSE`](LICENSE).

# Hetzner Viper

[English](README.md) | **Deutsch**

**VIPER** steht für **VI**rtual **PER**son: ein Code-Agent, der wie eine weitere Person im Team auf einem eigenen Rechner in Ihrem Repository arbeitet.

## Was ist Hetzner Viper?

Ein einzelnes Programm, `viper`, das mit einem Befehl einen entfernten Linux-Rechner bei einem Cloud-Anbieter (Hetzner Cloud, AWS, Google Cloud oder Azure) einrichtet. Auf diesem Rechner arbeitet der Code-Agent [Claude Code](https://github.com/anthropics/claude-code) remote in Ihrem Repository: Er liest und ändert Code, führt Builds und Tests aus und committet. Pro Git-Repository gibt es einen eigenen Rechner. Das Repository wird beim Einrichten automatisch geklont. Sie steuern den Agenten per SSH, VS Code Remote-SSH oder vom iPad, in einer tmux-Sitzung, die Verbindungsabbrüche übersteht. Wer fertig ist, löscht den Rechner wieder, und es entstehen keine laufenden Kosten.

Zielgruppe sind Entwicklerinnen und Entwickler mit eigenem Cloud-Konto: **Hetzner Cloud** (Standard) oder **AWS** (`PROVIDER=aws`, Amazon EC2). Google Cloud und Azure (`PROVIDER=gcp`, `azure`) gibt es, sie sind aber noch nicht in einem echten Konto erprobt. Der DNS-Name der Rechner kommt aus einer Cloudflare-Zone; ohne Cloudflare und ohne Domain (`DNS_PROVIDER=none`) erreichen Sie einen Rechner über seine Adresse.

## Was kann ich heute tun?

`viper` ist eine Datei (Linux, macOS) von der [Releases-Seite](https://github.com/pt9912/hetzner-viper/releases) oder mit Homebrew: `brew install pt9912/viper/viper`. Es braucht den OpenSSH-Client (`ssh`, `ssh-keygen`, `ssh-keyscan`, `scp`) und sonst nichts: kein Docker, `make`, `hcloud` oder `jq` (`git` dient der Repository-Prüfung und `code` dem Öffnen von VS Code, beides optional).

```bash
mkdir ~/viper && cd ~/viper        # hier liegt Ihre .env.local
viper find --cpu 4 --ram 16 --disk 80   # welcher Servertyp passt, bei welchem Anbieter, für wie viel (nur lesend, ohne Rechner)
viper setup                        # legt .env.local aus einer Vorlage an; DNS_DOMAIN (oder DNS_PROVIDER=none) und Zugangsdaten eintragen, dann erneut:
viper setup                        # Login-Key und lokale Host-CA anlegen
viper preflight web                # nur lesende Prüfung: Einstellungen, Cloud-Anbieter, Cloudflare, GitHub
viper up web --repo https://example.org/org/web.git   # Rechner "web" einrichten, Repo klonen
viper check web                    # nur lesend: DNS, Zugriff über den Namen wie ein anderes Gerät, Server-Zustand
viper ssh web                      # Shell auf dem Rechner (--cmd "..." für einen Befehl)
viper ssh web --tmux                # an die tmux-Sitzung "claude" anhängen (die von VS Code)
viper vscode web                   # VS Code erneut auf dem Rechner öffnen (wie viper up)
viper down web                     # auf ungesicherte Arbeit prüfen, dann Rechner "web" löschen
viper dns web                      # DNS-Name des Rechners "web" ausgeben
viper log [web] [--full]           # Ereignisse der Rechner oder die vollständige Ausgabe eines Rechners
```

| Funktion | Einstellung | Wirkung |
|---|---|---|
| Prüfung nur lesend | `viper preflight <name>` | Prüft vorab, bevor etwas angelegt wird: Einstellungen, lokales Setup, Cloud-Anmeldung (Hetzner-Token, AWS-Anmeldung, ...), Bestellbarkeit des Servertyps am Standort, Image, Cloudflare-Zone und -Eintrag, GitHub-Token und Repo-Zugriff. `viper up` führt es zuerst aus. |
| Cloud-Anbieter | `PROVIDER=hetzner` (Standard), `aws`, `gcp`, `azure` | Wo die Rechner laufen. Zugangsdaten, Servertyp, Standort und Image haben Standardwerte je Anbieter; AWS und Hetzner sind erprobt, `gcp` und `azure` noch nicht in einem echten Konto. |
| Ohne Cloudflare (optional) | `DNS_PROVIDER=none` | Kein DNS-Eintrag, keine Domain, kein Cloudflare-Konto: Ein Rechner ist mit `viper ssh <name>` oder über seine Adresse erreichbar (bei AWS zeigt `viper up` auch den Namen, den EC2 vergibt). |
| Eigene AWS-Tags (optional) | `AWS_TAGS`, `AWS_TAGS_SHARED` | Tags für Instanz und Systemplatte (`AWS_TAGS`) sowie für Schlüsselpaar und Security Group (`AWS_TAGS_SHARED`), gesetzt beim Anlegen, sodass auch Tag-Richtlinien (`aws:RequestTag`) den Start zulassen. Der Preflight prüft sie. |
| Mehrere Rechner | `viper up api …` neben `web` | Jeder Rechner hat eigenen Server und DNS-Namen `<SERVER_PREFIX>-<name>.<DNS_DOMAIN>` (Standard-Präfix `claude`). |
| GitHub | `VIPER_GH_TOKEN` | GitHub-PAT, am besten fein granular pro Rechner mit Contents und Pull requests: Read and write (ein für das lokale `gh` exportiertes `GH_TOKEN` wird bewusst nicht verwendet). Auf dem Server ist die GitHub-CLI `gh` angemeldet, und `git` pusht über HTTPS. Der Agent kann so auch Pull Requests anlegen. |
| Claude-Abo | `VIPER_CLAUDE_TOKEN` | Langlebiges Token Ihres Claude-Abos aus `claude setup-token`. Claude Code ist auf dem Server angemeldet, ohne `/login` nach jedem neuen Server. |
| Anderes Modell | `VIPER_ANTHROPIC_BASE_URL`, `VIPER_ANTHROPIC_AUTH_TOKEN`, `VIPER_CLAUDE_MODEL` | Richtet Claude Code auf dem Server auf eine Anthropic-kompatible Schnittstelle, etwa Ollamas Cloud (`https://ollama.com`) oder einen eigenen vLLM-Server. Der Preflight testet Schlüssel und Modell mit einer kleinen Anfrage. |
| Docker | immer | Engine, Buildx und Compose kommen aus Dockers eigenem apt-Repository und sind damit aktuell. |
| Git-Autor | `VIPER_GIT_USER_NAME`, `VIPER_GIT_USER_EMAIL` | Autor der Commits auf dem Server; mit `VIPER_GH_TOKEN` und ohne Wert vom GitHub-Konto (Name und noreply-Adresse). |
| code-server (optional) | `VIPER_CODE_SERVER=1`, `VIPER_CODE_SERVER_PASSWORD` | Installiert `code-server`, VS Code im Browser, das nur auf `127.0.0.1` lauscht. Zugriff über einen SSH-Tunnel (nützlich auf dem iPad). |
| Swap (optional) | `VIPER_SWAP` | Legt eine Swap-Datei an, als Faktor vom RAM des Servers (`1` = so viel wie RAM, `0.5` = die Hälfte) oder als Größe (`4G`); standardmäßig aus. Sie fängt Speicherspitzen (`docker build`, `npm install`) ab, statt dass der OOM-Killer zuschlägt. |
| Homebrew | `VIPER_HOMEBREW=0`, `VIPER_BREW_TAPS`, `VIPER_BREW_TRUST`, `VIPER_BREW_PACKAGES`, `VIPER_BREWFILE=1` | `brew` wird auf jedem neuen Server installiert und ist in allen Shells verfügbar (`VIPER_HOMEBREW=0` lässt es weg). Werkzeuge kommen aus den drei Listen (Tap, Trust, Installation, in dieser Reihenfolge) oder, mit `VIPER_BREWFILE=1`, aus einer `Brewfile` im Repository. |
| Befehle nach dem Clone | `VIPER_POST_CLONE` | Shell-Befehle, die auf dem Server im geklonten Repository laufen, nach dem Clone und nach Homebrew (Login-Shell als `builder`, Abbruch beim ersten Fehler, einmalig direkt nach dem Clone; nach einem Fehler versucht es das nächste `viper up` erneut). `VIPER_POST_CLONE_ON_ERROR=retry` (Standard), `ignore` oder `abort` bestimmt, was ein Fehler bewirkt. Log: `~/.config/viper/post-clone.log`. |
| Servertyp suchen | `viper find --cpu N --ram GB --disk GB` | Sucht die günstigsten Servertypen mit mindestens so viel Ausstattung bei Hetzner, AWS, Azure und Google Cloud und zeigt sie in **einer Tabelle auf einer Basis**: in Euro (US-Dollar mit dem Kurs der Europäischen Zentralbank), mit demselben Mehrwertsteuersatz für alle (`--vat`), nach Preis sortiert, mit der Systemplatte im Preis und den Einstellungen zum Übernehmen. Kein Anbieter braucht ein Konto: Die öffentlichen Preislisten werden einmal am Tag gelesen und in `~/.cache/viper` gemerkt (ein Hetzner-Token oder ein Google-Projekt wird, falls gesetzt, zuerst genutzt und gibt die genauen Preise Ihres Projekts). Die Gebühr für die öffentliche IPv4-Adresse steckt in den Preisen. Nur lesend. |
| Sicher löschen | `viper down <name>` | Prüft den Git-Stand aller Repositories und Worktrees im Arbeitsverzeichnis, im Home- und in den Temp-Verzeichnissen (uncommittete Änderungen, Commits auf keinem Remote auf allen Branches, Stash-Einträge), meldet Einträge auf oberster Ebene des Arbeitsverzeichnisses, die zu keinem Repository gehören (Dateien außerhalb von Repositories an anderen Orten prüft es nicht), listet ignorierte Dateien auf und fragt bei ungesicherter Arbeit zusätzlich nach `DISCARD`. |

Die vollständige Anleitung mit allen Aufgaben, Einstellungen und der Fehlerbehebung steht im [Benutzerhandbuch](docs/user/handbuch.md). Für den ersten echten Lauf beginnen Sie dort mit dem Abschnitt „Erster Testlauf mit einem kleinen Server“ (Servertyp `cpx02`, wenige Cent pro Stunde).

## Warum Hetzner Viper?

Ein Code-Agent wie Claude Code soll auf einem Rechner arbeiten, der nicht der eigene Laptop ist: mit viel Leistung, isoliert von den eigenen Daten und Zugängen, dauerhaft erreichbar per tmux und vom iPad bedienbar. Ein Dauerbetrieb lohnt sich dafür nicht. Von Hand sind Server, Firewall, DNS, SSH-Härtung und Repository-Setup bei jedem Start mühsam und fehleranfällig. `viper` macht daraus einen Befehl zum Starten und einen zum Löschen.

## Kerngedanke

Rechner sind Wegwerfware: Alles, was zählt, liegt im Git-Repository. Deshalb gibt es bewusst keinen persistenten Datenträger, und `viper down` schützt vor dem einzigen Risiko, das daraus folgt, nämlich vor ungepushter Arbeit.

## Was macht es vertrauenswürdig?

- **Tests:** `make check` (gofmt, vet, golangci-lint und über 300 Tests, alles in Docker; CI bei jedem Push; `make help` zeigt die Ziele, `make build` baut `dist/viper` ohne Go-Installation). Sie decken Einrichtung, Bereitschaftsprüfung, Mehrrechner-Betrieb, Git-Clone, Firewall- und DNS-Schutz, die Sicherheitsabfragen von `viper down`, den Abbruch mit Ctrl-C und den Parser für `.env.local` (verglichen mit echtem Bash) ab. Die erzeugten cloud-init-User-Data werden mit Referenzdateien verglichen und als YAML gelesen.
- **Schutz fremder Ressourcen:** `viper` ändert und löscht nur DNS-Einträge, die es selbst angelegt hat.
- **Echte Läufe:** Die End-to-End-Tests (Build-Tag `e2e`) richten einen Rechner in einem echten AWS-Konto ein und löschen ihn wieder (`viper up` und `viper down`, x86 und Arm, ohne Cloudflare); Architektur und Tests beschreibt [`docs/maintainer/architektur.md`](docs/maintainer/architektur.md).
- **Handbuch:** [`docs/user/handbuch.md`](docs/user/handbuch.md) mit Versions- und Änderungshistorie.
- **Releases:** Versionen stehen als Tags auf der [Releases-Seite](https://github.com/pt9912/hetzner-viper/releases) mit den `viper`-Binaries und Prüfsummen; zum Erstellen siehe [`docs/maintainer/releasing.md`](docs/maintainer/releasing.md).
- **Lizenz:** MIT, siehe [`LICENSE`](LICENSE).

## Sicherheit und Grenzen

- SSH nur mit Public-Key (kein Passwort, kein Root-Login). Standardmäßig ist Port 22 aus dem ganzen Internet offen. Schränken Sie `SSH_ALLOWED_CIDRS` ein, wenn Sie nur von festen Netzen arbeiten.
- Der Benutzer `builder` hat sudo ohne Passwort und Docker-Zugriff, das entspricht Root. Nutzen Sie nur vertrauenswürdige Repositories.
- Serverseitig wird ein CA-signiertes SSH-Host-Zertifikat verwendet (sieben Tage gültig, keine automatische Erneuerung). Der private Host-Key wird dafür in den User-Data des Cloud-Anbieters (Hetzner, EC2) übertragen und ist dort für Administratoren des Projekts und für Prozesse auf dem Server lesbar. Der CA-Schlüssel bleibt lokal. Der einrichtende Rechner hinterlegt den Host-Schlüssel jedes Servers und vertraut nur diesem. Ein abgeflossener Schlüssel eines früheren Servers wird so abgelehnt, und die Verbindung funktioniert auch nach Ablauf des Zertifikats.
- Der Login-Schlüssel hat keine Passphrase.
- **Alles auf dem Server geht beim Löschen verloren**, auch eine Claude-Anmeldung per `/login` (mit `VIPER_CLAUDE_TOKEN` ist jeder neue Server automatisch wieder angemeldet).
- Die SSH-Konfiguration jedes Rechners leitet Ihren ssh-agent und X11 nie an den Server weiter, auch wenn Ihre eigene `~/.ssh/config` das tut.
- `viper` legt kostenpflichtige Ressourcen an. Legen Sie API-Tokens nie in Git ab.
- Nutzen Sie ein eigenes Projekt oder Konto beim Cloud-Anbieter und probieren Sie neue Einstellungen zuerst mit `viper preflight` und einem kleinen Server aus.

## Voraussetzungen

Linux oder macOS, das Binary `viper` und ein OpenSSH-Client. VS Code Remote-SSH nutzt den lokalen OpenSSH-Client; ohne VS Code nutzen Sie `viper ssh`. Dazu ein Konto bei einem Cloud-Anbieter (ein Hetzner-Cloud-API-Token mit Lesen und Schreiben oder, mit `PROVIDER=aws`, eine AWS-Anmeldung per `aws login`; Google Cloud und Azure beschreibt das Handbuch) und, für den DNS-Namen, ein Cloudflare-Token mit **Zone DNS Edit/Read** samt Zone-ID (mit `DNS_PROVIDER=none` nicht nötig).

## Lizenz

MIT, siehe [`LICENSE`](LICENSE).

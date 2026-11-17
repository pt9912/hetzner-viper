# Benutzerhandbuch: Hetzner Viper

Version: 1.195  
Software-Version: v0.33.0  
Stand: 16.11.2026  

## 1. Einleitung

### Zweck der Software

Das Programm `viper` richtet mit einem Befehl einen Arbeitsrechner ein: bei einem Cloud-Anbieter (Hetzner Cloud, AWS (Amazon EC2), Google Cloud oder Azure), als Pod in einem Kubernetes-Cluster oder als Container auf dem Docker-Host Ihres eigenen Computers. Die beiden letzten Wege brauchen kein Cloud-Konto und lassen sich lokal ausprobieren, auch mit einem k3d-Cluster (Abschnitt „Lokal arbeiten, ohne Cloud-Konto“). Hetzner und AWS sind in echten Konten erprobt; bei Google Cloud und Azure ist das nicht belegt (Kapitel 4). Kubernetes ist in k3d erprobt (nicht in EKS, GKE oder AKS), Docker Compose auf einem lokalen Linux-Docker-Host (nicht auf Docker Desktop). Auf diesem Rechner arbeitet der Code-Agent Claude Code als „virtuelle Person“ (VIPER: VIrtual PERson) in Ihrem Repository. Sie arbeiten darauf über VS Code (Remote-SSH) oder über ein SSH-Terminal, zum Beispiel vom iPad.

Pro Git-Repository richten Sie einen eigenen Rechner ein. Beim Einrichten wird das Repository automatisch geklont. Wenn Sie fertig sind, löschen Sie den Rechner wieder. Alle Dateien auf dem Rechner werden dabei gelöscht. Sichern Sie Ihre Arbeit deshalb vorher mit `git push`.

### Zielgruppe

Dieses Handbuch richtet sich an Entwicklerinnen und Entwickler, die auf einem Linux-Rechner arbeiten und die Kommandozeile bedienen können.

### Voraussetzungen

Auf Ihrem lokalen Rechner (Linux, macOS oder Windows; Windows ist nur teilweise getestet, siehe „`viper` installieren“):

* Das Programm `viper` (eine einzelne Datei aus dem Release, siehe „`viper` installieren“).
* Der OpenSSH-Client mit `ssh`, `ssh-keygen`, `ssh-keyscan` und `scp`. Mehr brauchen Sie bei einem Cloud-Anbieter nicht: `hcloud`, `jq`, `curl`, Docker und `make` sind nicht nötig. Docker brauchen Sie nur für Rechner als Container oder Pod (der Docker-Host von `PROVIDER=compose`, das Image für `kubernetes`; bei k3d außerdem das Programm `k3d`).
* Optional VS Code mit der Erweiterung **Remote-SSH**. Sie nutzt den OpenSSH-Client Ihres Rechners. Ohne VS Code verbinden Sie sich mit `viper ssh <rechner>`; VS Code öffnen Sie später mit `viper vscode <rechner>` erneut.

Bei den Anbietern:

* Ein Konto bei einem Cloud-Anbieter, außer Sie arbeiten mit Containern auf Ihrem Docker (`PROVIDER=compose`) oder in einem vorhandenen Kubernetes-Cluster (`PROVIDER=kubernetes`, etwa k3d); beides steht im Abschnitt „Lokal arbeiten, ohne Cloud-Konto“. Bei **Hetzner Cloud** ein Projekt mit einem **API-Token mit Lese- und Schreibrechten**, bei **AWS** eine Anmeldung (`aws login` oder SSO) mit Rechten für EC2; Google Cloud und Azure siehe Kapitel 4.
* Eine Domain bei Cloudflare, wenn `viper` den DNS-Namen der Rechner anlegen soll (Standard). Dafür brauchen Sie einen **API-Token** mit den Rechten **Zone DNS Edit** und **Zone DNS Read** und die **Zone-ID** der Domain. Ohne Domain und Cloudflare arbeiten Sie mit `DNS_PROVIDER=none` (Kapitel 4): Dann erreichen Sie die Rechner über ihre Adresse.

> **Kosten:** `viper` legt kostenpflichtige Ressourcen an. Der Anbieter kann zusätzlich die öffentliche IPv4-Adresse berechnen (Hetzner, AWS). Ein gelöschter Server verursacht keine weiteren Kosten. Container auf Ihrem Docker-Host und in einem lokalen k3d-Cluster kosten nichts außer den Mitteln Ihres Computers; ein Kubernetes-Cluster rechnet sein Betreiber nach eigenen Regeln ab.

> **Datenverlust:** Es gibt keinen separaten Datenträger. Die Arbeitsdateien liegen auf der Platte des Servers und gehen beim Löschen verloren. Was nicht gepusht ist, ist weg. Bei einem Pod oder Container liegen das Heimverzeichnis und Homebrew auf Volumes, die das Ersetzen des Pods oder einen Neustart des Containers überstehen; `viper down` löscht sie mit allen Daten.

> **Sicherheit:** Legen Sie API-Tokens nie in Git ab. Der Benutzer `builder` auf dem Server hat Root-Rechte (sudo ohne Passwort, Docker-Gruppe). Arbeiten Sie dort nur mit vertrauenswürdigen Repositories. In einem Pod oder Container hat `builder` kein Root und kein `sudo`; dafür läuft das Docker im Rechner standardmäßig in einem **privilegierten** Container, der Root auf dem Host bedeuten kann: Nutzen Sie das nur auf Ihrem eigenen Computer oder Cluster (Abschnitte zu Kubernetes und Docker Compose).

## 2. Erste Schritte

### Überblick

Sie haben kein Cloud-Konto oder wollen erst einmal ausprobieren? Dann beginnen Sie mit dem Abschnitt „Lokal arbeiten, ohne Cloud-Konto (Docker Compose oder k3d)“ in Kapitel 3. Alle Befehle unten gelten für jeden Anbieter.

| Befehl | Aufgabe |
|---|---|
| `viper setup` | Legt Ihre `.env.local` (aus einer vorbereiteten Vorlage), Ihren Login-Schlüssel und die lokale Host-CA an (einmalig). |
| `viper preflight <rechner>` | Prüft vorab nur lesend Einstellungen, Setup, Ihren Cloud-Anbieter, Cloudflare (falls verwendet) und GitHub. Legt nichts an. `viper up` führt es zuerst aus. |
| `viper image build [--tag NAME:TAG] [--k3d CLUSTER]` | Baut das Image der Rechner für `PROVIDER=compose` und `PROVIDER=kubernetes` mit Docker (bei k3d gleich in den Cluster). Nur für diese beiden Anbieter, siehe „Lokal arbeiten, ohne Cloud-Konto“. |
| `viper up <rechner>` | Richtet einen Rechner ein, setzt den DNS-Eintrag, legt das Arbeitsverzeichnis an und klont das Repository. |
| `viper check <rechner>` | Prüft einen laufenden Rechner nur lesend: DNS, Zugriff über den Namen wie ein anderes Gerät, Zustand des Servers. |
| `viper ssh <rechner>` | Öffnet eine Shell auf dem Rechner oder führt mit `--cmd` einen Befehl aus, mit der SSH-Konfiguration des Rechners. Mit `--tmux` hängt sich die Verbindung an die tmux-Sitzung `claude` an (oder legt sie im Arbeitsverzeichnis an), dieselbe wie in den VS-Code-Terminals, und setzt deren Statuszeile auf den Rechner („[web (dev)]“); `--tmux` braucht ein Terminal und lässt sich nicht mit `--cmd` kombinieren. |
| `viper vscode <rechner>` | Öffnet VS Code (Remote-SSH) auf dem laufenden Rechner im Arbeitsverzeichnis, so wie `viper up` es nach dem Einrichten tut. Braucht den Befehl `code`. Die Terminals dort hängen sich an die tmux-Sitzung `claude`. |
| `viper terminal <rechner>` | Öffnet ein **Terminalfenster dieses Computers** mit `viper ssh <rechner> --tmux`, also mit der tmux-Sitzung `claude`. Das Fenster ist ein Programm des Desktops: das Terminal von macOS, Windows Terminal, unter Linux das erste installierte von `gnome-terminal`, `konsole`, `xfce4-terminal`, `kitty`, `alacritty`, `wezterm` und `xterm` (die Umgebungsvariable `VIPER_TERMINAL` nennt eines davon). Ohne Desktop meldet der Befehl den Fehler. Der **Titel des Fensters** nennt den Rechner und, wenn er in einer Gruppe steht, die Gruppe („web (dev)“), und die **Statuszeile von tmux** unten im Fenster zeigt dasselbe („[web (dev)]“); die Statuszeile setzt jedes `viper ssh --tmux`, auch am Terminal. Sie gilt für die ganze Sitzung `claude` (auch für die VS-Code-Terminals darin) und **ersetzt eine eigene `status-left` aus Ihrer `~/.tmux.conf`** bei jedem Aufruf. Mit `--here` läuft derselbe Aufruf im eigenen Terminal: `ssh` läuft als Kind, und bei einem Fehler bleibt die Meldung stehen, bis Sie Enter drücken (so verschwindet sie nicht mit dem Fenster). |
| `viper down <rechner> [--force] [--delete-registry\|--keep-registry]` | Prüft den Git-Stand, warnt vor ungesicherter Arbeit und löscht Server und DNS-Eintrag. Mit dem letzten Rechner eines k3d-Clusters, den `viper up` angelegt hat, auch den Cluster; die Registry nach Frage oder Option. |
| `viper dns <rechner>` | Gibt den DNS-Namen des Rechners aus (`<SERVER_PREFIX>-<rechner>.<DNS_DOMAIN>`), ohne Zugriff auf den Anbieter oder Cloudflare. Mit `DNS_PROVIDER=none` gibt es keinen Namen; der Befehl nennt die zuletzt gespeicherte Adresse. |
| `viper find` | Sucht bei allen Anbietern Server-Typen, die mindestens die gewünschten CPUs, den RAM und die Platte haben, und zeigt die günstigsten mit Preis. Liest nur und braucht keinen Rechner. |
| `viper log [<rechner>]` | Zeigt die lokalen Protokolle: Ereignisse aller Rechner, eines Rechners oder mit `--full` dessen vollständige Ausgabe. |
| `viper ui [--port N] [--no-open] [--readonly\|--edit]` | Startet eine Weboberfläche auf `127.0.0.1` im Browser: Übersicht der Rechner, Prüfen, Vorprüfung/Up/Down, NATS, Einstellungen und Protokolle; mit `--edit` auch neue Rechner und Gruppen anlegen. Siehe „Die Oberfläche im Browser“. |
| `viper up --group <gruppe>`, `viper down --group <gruppe>`, `viper check --group <gruppe>`, `viper preflight --group <gruppe>`, `viper ssh --group <gruppe> --cmd …` | Dieselben Befehle für alle Rechner einer Gruppe aus `groups/<gruppe>.env`, gleichzeitig (siehe „Eine Gruppe von Rechnern gemeinsam starten und beenden“). |
| `viper mcp-binary --arch amd64\|arm64` | Schreibt den in `viper` eingebauten MCP-Server `viper-nats-mcp` für die Architektur nach stdout (`> datei`), zum Ansehen, Prüfen oder als `NATS_MCP_BINARY`. Zeigt nichts auf dem Terminal. |
| `viper nats peers\|topics\|listen\|send\|broadcast\|request\|fetch\|init\|revoke <gruppe> …` | Sendet Nachrichten (und Bilder, PDFs und ZIP-Dateien) an die Rechner einer NATS-Gruppe (`--file DATEI` statt des Textes) und liest mit, als einer der Rechner der Gruppe (siehe „Nachrichten an eine NATS-Gruppe senden“); `fetch` holt eine Datei, `init` und `revoke` richten Ihren eigenen Schlüssel als Betreiber ein und entfernen ihn. |
| `viper help`, `viper <befehl> --help`, `viper version` | Zeigt alle Befehle in Gruppen; die Hilfe eines Befehls mit seinen Optionen (jede erklärt), Beispielen und dem passenden Abschnitt des Handbuchs (`viper check --help`, `viper nats send --help`; auch `viper help check`); die Version. |

### Wie ein Rechner benannt wird

Jeder Rechner hat einen Namen, den Sie beim Start angeben (`<rechner>`). Erlaubt sind Kleinbuchstaben, Ziffern und Bindestriche. Der Name darf nicht mit einem Bindestrich beginnen oder enden. Aus dem Namen entstehen:

| Ressource | Name |
|---|---|
| Server | `<SERVER_PREFIX>-<rechner>` |
| DNS-Name | `<SERVER_PREFIX>-<rechner>.<DNS_DOMAIN>` |
| SSH-Alias | `hetzner-<SERVER_PREFIX>-<rechner>` |

Login-Schlüssel, Firewall und Host-CA teilen sich alle Rechner.

## 3. Aufgaben ausführen

### `viper` installieren

`viper` ist ein einzelnes Programm. Es richtet Rechner ein, prüft sie, verbindet sich mit ihnen und löscht sie wieder. Es läuft direkt auf Ihrem Rechner, ohne Docker, ohne `make` und ohne ein geklontes Repository.

#### Voraussetzung

* Linux, macOS oder Windows (dort nur teilweise getestet; zuverlässiger ist `viper` in WSL mit dem Linux-Binary).
* Der OpenSSH-Client mit `ssh`, `ssh-keygen`, `ssh-keyscan` und `scp`. `viper` meldet es, bevor es etwas anlegt, wenn ein Programm fehlt.

#### Vorgehen

**Mit Homebrew** (Linux und macOS): `brew install pt9912/viper/viper`, dann `viper version` (Update: `brew upgrade viper`). Sonst von Hand:

1. Laden Sie von der Release-Seite (`https://github.com/pt9912/hetzner-viper/releases`) die Datei für Ihr System, zum Beispiel `viper_v0.33.0_linux_amd64` (`linux` oder `darwin`; `amd64` oder `arm64`, bei Apple Silicon `arm64`; für Windows gibt es nur `viper_v0.33.0_windows_amd64.exe`, siehe Hinweis zu Windows unten), und `SHA256SUMS`.
2. Prüfen Sie die Datei und machen Sie sie ausführbar:
   ```bash
   grep viper_v0.33.0_linux_amd64 SHA256SUMS | sha256sum --check      # macOS: ... | shasum -a 256 --check
   chmod +x viper_v0.33.0_linux_amd64
   mv viper_v0.33.0_linux_amd64 ~/bin/viper            # ein Verzeichnis in Ihrem PATH
   ```
3. Prüfen Sie die Installation: `viper version`.

#### Ergebnis

`viper help` listet alle Befehle. Eine neuere Version installieren Sie genauso; Ihre Einstellungen und Rechner bleiben unberührt, denn sie liegen in Ihrer `.env.local` und in `~/.local/state/viper`.

#### Hinweise

* **Arbeitsverzeichnis:** `viper` liest `.env.local` (und `groups/<gruppe>.env` und `machines/<rechner>.env.local`) im aktuellen Verzeichnis oder in dem, das Sie mit `--dir <verzeichnis>` angeben. `viper setup` legt eine fehlende `.env.local` aus einer eingebauten Vorlage an. Ein relativer `SSH_KEY` gilt relativ zu diesem Verzeichnis.
* **Einstellungen:** `viper` versteht die Schreibweise der Datei (`NAME=wert`, Anführungszeichen, `$HOME`, `${NAME}`, mehrzeilige Werte, Kommentare) und lehnt anderes mit Zeilennummer ab, ohne die Zeile selbst zu zeigen (sie kann ein Token enthalten): Befehle und Ersetzungen wie `$(befehl)`, nicht gesetzte Variablen, `~benutzer`, `$'…'` und Windows-Zeilenenden (CRLF). Setzen Sie solche Werte als festen Text in Anführungszeichen und speichern Sie die Datei mit Unix-Zeilenenden.
* **Fragen beim Löschen:** `viper down` fragt `DISCARD` bei ungesicherter Arbeit, `DELETE` zum Löschen und `CLEANUP` bei einem Rechner, den es beim Anbieter nicht mehr gibt. Die Antworten dürfen auch per Pipe kommen; eine leere Antwort, das Ende der Eingabe und eine letzte Zeile ohne Zeilenende bestätigen nichts.
* **Verbindung:** `viper ssh` ersetzt sich selbst durch `ssh`: Signale und Exit-Code sind die von `ssh`.
* **Prüfungen:** `viper preflight` und `viper check` sprechen die APIs Ihres Cloud-Anbieters, von Cloudflare (falls verwendet) und (falls gesetzt) GitHub und Ihrem Claude-Gateway direkt an. `viper check` sendet ein Prüfskript per SSH auf den Server und ändert dort nichts. `viper dns` prüft zusätzlich Rechnername, `DNS_DOMAIN`, `SERVER_PREFIX` und die Länge des Servernamens.
* **Server-Skripte:** Die Programme, die auf dem Server laufen (Einrichtung, Prüfung, Sicherheitsprüfung vor dem Löschen), sind in `viper` eingebaut.
* **VS Code:** `viper up` öffnet VS Code, wenn `code` vorhanden ist, ohne dass VS Code Ihre Tokens erbt. Mit `--no-code` öffnet es VS Code nicht.
* **Abbruch:** Ein erstes Ctrl-C beendet den laufenden Befehl und wird protokolliert (`interrupted`); ein zweites beendet `viper` sofort. Ein abgebrochenes `viper up` lässt den Server stehen; ein erneutes `viper up` setzt die Einrichtung fort.
* **macOS:** Ein Binary aus dem Browser versieht macOS mit einer Quarantäne-Markierung, die das Starten verhindert. Entfernen Sie sie mit `xattr -d com.apple.quarantine ~/bin/viper`. Auf macOS ist `viper` bisher nicht getestet.
* **Windows:** Es gibt ein Binary für Windows (`windows_amd64.exe`), es ist aber nur teilweise getestet; nicht alle Funktionen sind dort geprüft. Voraussetzung ist ein OpenSSH-Client (`ssh`, `ssh-keygen`) im `PATH`. Pfade in `.env.local` schreiben Sie dort mit `/` (`SSH_KEY=C:/Users/Name/.ssh/hetzner-build`); ein `\` gilt in dieser Schreibweise als Fluchtzeichen und geht verloren. Zuverlässiger ist `viper` innerhalb von WSL mit dem Linux-Binary.
* **Quellcode:** Das Release enthält auch den Quellcode. Mit Docker und `make` bauen Sie `viper` selbst, ohne Go zu installieren: `make build` legt `dist/viper` ab (`make build GOOS=darwin GOARCH=arm64` für ein anderes System, für Windows `dist/viper.exe`; `make dist` baut alle Plattformen wie ein Release; `make help` zeigt alle Ziele). Mit installiertem Go geht auch `go build ./cmd/viper`. Für die Benutzung ist das nicht nötig.

### Erstmalige Einrichtung

#### Voraussetzung

Sie haben die Voraussetzungen aus Kapitel 1 erfüllt.

#### Vorgehen

1. Legen Sie ein Verzeichnis für Ihre Einstellungen an und wechseln Sie hinein, zum Beispiel `mkdir ~/viper && cd ~/viper`.
2. Führen Sie `viper setup` aus. Es legt die Datei `.env.local` aus der Vorlage an (Rechte `600`) und weist darauf hin, dass `DNS_DOMAIN` fehlt.
3. Öffnen Sie `.env.local` und tragen Sie bei `DNS_DOMAIN` Ihre Domain ein. Ohne Cloudflare tragen Sie stattdessen `DNS_PROVIDER=none` ein; dann entfällt `DNS_DOMAIN`.
4. Führen Sie `viper setup` erneut aus. Es legt Login-Schlüssel und Host-CA an und gibt den öffentlichen Host-CA-Schlüssel aus.
5. Setzen Sie die Zugangsdaten in Ihrer Shell (`viper setup` zeigt die Zeilen für Ihren Anbieter; das Beispiel gilt für Hetzner mit Cloudflare):
   ```bash
   export HCLOUD_TOKEN='...'
   export CLOUDFLARE_API_TOKEN='...'
   export CLOUDFLARE_ZONE_ID='...'
   ```
   Alternativ tragen Sie die Werte in `.env.local` ein (Zeilen mit `#` am Anfang freischalten). Bei AWS, Google Cloud und Azure melden Sie sich stattdessen mit `aws login`, `gcloud auth application-default login` bzw. `az login` an. Die Cloudflare-Werte entfallen mit `DNS_PROVIDER=none`.

#### Ergebnis

Es existieren Ihr Login-Schlüssel (Standard: `~/.ssh/hetzner-build`) und die Host-CA (`~/.ssh/hetzner_claude_host_ca`). Die Datei `.env.local` wird nicht in Git eingecheckt.

#### Hinweise

* Der private CA-Schlüssel darf nie auf den Server oder ein anderes Gerät kopiert werden.
* Die Login-Schlüssel haben keine Passphrase. Schützen Sie die Dateien im Ordner `~/.ssh`.

### Vorab prüfen (Preflight)

#### Voraussetzung

Die erstmalige Einrichtung ist abgeschlossen.

#### Vorgehen

1. Führen Sie `viper preflight <rechner> [--repo <url>]` aus.
2. Lesen Sie die Ausgabe. Jede Zeile beginnt mit `ok`, `info` oder `FAIL`.

Geprüft werden:

* **Einstellungen:** Alle Pflichtwerte sind gesetzt, `DNS_DOMAIN` (mit Cloudflare) und `SSH_ALLOWED_CIDRS` haben ein gültiges Format.
* **Lokales Setup:** Login-Schlüssel und Host-CA sind vorhanden, die CA gilt für `*.<DNS_DOMAIN>` (mit `DNS_PROVIDER=none` entfällt das), die Dateien aus `GIT_DEPLOY_KEY_FILE` und `IPAD_SSH_PUBLIC_KEY_FILE` sind lesbar.
* **Cloud-Anbieter:** Die Anmeldung gilt (bei Hetzner: das Token kann das Projekt lesen). Servertyp bzw. Instanztyp, Standort bzw. Region und Image (für die Architektur des Typs) existieren. Bei Hetzner ist der Servertyp am Standort derzeit bestellbar. Der Login-Schlüssel kollidiert nicht mit einem anderen Schlüssel gleichen Namens (Hetzner, AWS).
* **Cloudflare (entfällt mit `DNS_PROVIDER=none`):** Die Zone ist lesbar, `DNS_DOMAIN` liegt darin, und es gibt keinen fremden DNS-Eintrag für den Rechner.
* **GitHub (wenn `VIPER_GH_TOKEN` gesetzt ist):** Das Token ist gültig, ein klassisches Token hat die Scopes `repo` und `read:org`, und es hat Zugriff auf ein GitHub-Repository aus `REPO_URL` und darf dorthin pushen.
* **Repository:** Ein HTTPS- oder lokales Repository ist erreichbar. Eine SSH-Adresse braucht `GIT_DEPLOY_KEY_FILE` (dann prüft erst der Clone auf dem Server den Zugriff) oder, für GitHub, `VIPER_GH_TOKEN` (dann wird über HTTPS geklont); sonst lehnt der Preflight sie ab.

#### Ergebnis

`Preflight OK.` oder eine Liste der Probleme. Es wurde nichts angelegt oder geändert.

#### Hinweise

* Schreibrechte (beim Anbieter Server anlegen, bei Cloudflare DNS bearbeiten) lassen sich nur lesend nicht prüfen. Fehlen sie, scheitert erst `viper up`.
* Fehlen in `.env.local` die Werte `SERVER_TYPE`, `SERVER_LOCATION`, `SERVER_IMAGE` oder `SSH_KEY`, gelten die Standardwerte aus Kapitel 4.
* Zeilen mit `WARN` blockieren nicht. Der Preflight warnt etwa, wenn ein Servertyp an einem Standort abgekündigt ist oder `.env.local` für andere Benutzer lesbar ist (dann empfiehlt er `chmod 600 .env.local`, weil die Datei Tokens enthalten kann).

### Erster Testlauf mit einem kleinen Server

Bevor Sie mit einem großen Server arbeiten, prüfen Sie die Einrichtung einmal mit dem kleinsten Typ. Ein Testlauf von etwa einer Stunde kostet nur Cent-Beträge, weil Hetzner stundenweise abrechnet.

#### Voraussetzung

Die erstmalige Einrichtung ist abgeschlossen. Am besten nutzen Sie ein eigenes Projekt oder Konto beim Anbieter nur für diese Rechner. Bei Hetzner gilt ein API-Token immer für das ganze Projekt.

#### Vorgehen

1. Tragen Sie in die Einstellungsdatei des Testrechners `machines/test.env.local` die Zeile `SERVER_TYPE=cpx02` ein. Gibt es die Datei noch nicht, kopieren Sie vorher `machines/example.env.local.example` dorthin (die Vorlage legt `viper setup` neben Ihrer `.env.local` an). Dort können Sie auch `REPO_URL` setzen, dann brauchen Sie die URL in den folgenden Befehlen nicht. `cpx02` hat 1 vCPU, 1 GB RAM und 20 GB Platte.
2. Prüfen Sie vorab: `viper preflight test` (mit URL: `viper preflight test --repo https://github.com/<konto>/<repo>.git`). Meldet die Prüfung, dass `cpx02` in `fsn1` derzeit nicht bestellbar ist, ergänzen Sie in derselben Datei `SERVER_LOCATION=nbg1` oder `hel1`, oder nehmen Sie `SERVER_TYPE=cpx12` (2 GB RAM).
3. Starten Sie den Rechner: `viper up test` (oder mit `--repo …` wie oben).
4. Prüfen Sie den Rechner: `viper check test` (siehe „Einen laufenden Rechner prüfen“).
5. Löschen Sie den Rechner wieder: `viper down test` und bestätigen Sie mit `DELETE`.

#### Ergebnis

Sie wissen, dass Tokens, DNS, SSH-Vertrauen, Einrichtung, Clone und Löschen in Ihrem Konto funktionieren. In der Konsole Ihres Anbieters und bei Cloudflare ist danach nichts mehr vom Testrechner übrig. SSH-Schlüssel und Firewall bleiben für die nächsten Rechner bestehen.

#### Hinweise

* 1 GB RAM reicht für den Funktionstest. Für echte Arbeit des Agenten mit Builds und Tests ist der Speicher zu knapp. Nutzen Sie dafür einen größeren Typ.
* Bricht `viper up` ab, bleibt der Server bestehen. Führen Sie `viper up test` nach dem Beheben erneut aus, oder löschen Sie ihn mit `viper down test`. Ein Server gilt erst nach einem vollständig gelaufenen `viper up` als eingerichtet (vermerkt lokal und auf dem Server, gilt also auch für andere Computer); bis dahin wartet ein erneuter Lauf wie bei einem neuen Server, auch auf den Neustart nach den Updates, aber höchstens etwa drei Minuten.

### Lokal arbeiten, ohne Cloud-Konto (Docker Compose oder k3d)

Sie können `viper` auch ohne Cloud-Konto ausprobieren und nutzen: Der Rechner ist dann ein Container auf dem Docker Ihres eigenen Computers (`PROVIDER=compose`) oder ein Pod in einem lokalen k3d-Cluster (`PROVIDER=kubernetes`). Es entstehen keine Kosten, kein DNS-Eintrag und kein Server im Netz. Dieser Abschnitt ist die kurze Fassung; die vollständigen Aufgaben stehen weiter unten („Einen Rechner als Container auf dem eigenen Docker-Host betreiben“ und „Einen Rechner als Pod in einem Kubernetes-Cluster betreiben“).

#### Voraussetzung

* Die „Erstmalige Einrichtung“ ist gemacht (`viper setup`: Login-Schlüssel und Host-CA). Mit `DNS_PROVIDER=none` brauchen Sie weder eine Domain noch Cloudflare.
* Docker auf Ihrem Computer. Für Compose zusätzlich das Plugin `docker compose`, für k3d das Programm `k3d` und `kubectl`.

#### Vorgehen

1. **Image beschaffen.** Diesen Schritt können Sie überspringen: Ohne `COMPOSE_IMAGE` bzw. `K8S_IMAGE` nimmt `viper` das veröffentlichte Image seiner Version, `ghcr.io/pt9912/viper-machine:<Version>`; Compose und ein k3d-Cluster laden es selbst. Für ein eigenes Image oder eine andere Version gehen Sie auf einem von zwei Wegen:
   * **Laden** (schnell): `docker pull ghcr.io/pt9912/viper-machine:v0.33.0`, mit der Version, die `viper version` meldet. Bei Docker Compose können Sie das Laden `viper up` überlassen: Der Name in `COMPOSE_IMAGE` genügt. Das Image ist öffentlich und mehrere GB groß; das Image gibt es für **amd64 und arm64**, Docker wählt die passende Architektur. Der Name ist `ghcr.io/pt9912/viper-machine:v0.33.0`. Bei k3d (vorher `k3d cluster create viper`) lädt der Cluster das öffentliche Image beim ersten Start selbst, das dauert etwa eine halbe Minute; Sie müssen es nicht einspielen. **Jeder Knoten lädt es für sich**, auf dem ein Rechner läuft: Hat der Cluster mehrere Knoten, laden Sie es mehrmals aus dem Internet. Dann spielen Sie das geladene Image mit `k3d image import ghcr.io/pt9912/viper-machine:v0.33.0 -c viper` ein (kopiert es auf alle Knoten, ohne Download) oder nutzen eine gemeinsame Registry (unten).
   * **Bauen:** `viper image build` (für k3d: erst `k3d cluster create viper`, dann `viper image build --k3d viper`). Das dauert beim ersten Mal einige Minuten. Die Ausgabe nennt den Namen des Images.
2. **Einstellungen** in `machines/lokal.env.local` (die Zeile mit dem Image brauchen Sie nur für ein selbst gebautes oder anderes Image):

   Docker Compose:

   ```
   PROVIDER=compose
   DNS_PROVIDER=none
   SERVER_LOCATION=default
   # COMPOSE_IMAGE=<Name aus Schritt 1>   (nur für ein selbst gebautes oder anderes Image)
   COMPOSE_DOCKER=dind
   SERVER_TYPE=2x4
   ```

   k3d:

   ```
   PROVIDER=kubernetes
   DNS_PROVIDER=none
   SERVER_LOCATION=k3d-viper
   # K8S_IMAGE=<Name aus Schritt 1>   (nur für ein selbst gebautes oder anderes Image)
   SERVER_TYPE=2x4
   ```
3. `viper preflight lokal`, dann `viper up lokal`, danach `viper ssh lokal` und `viper check lokal`. Zum Aufräumen: `viper down lokal`.

#### Ergebnis

`viper up` meldet „The machine is the container …“ (Compose) oder „… the pod …“ (k3d) und am Ende „Ready“. Auf dem Rechner laufen Docker, Homebrew und Claude Code wie auf einem Server in der Cloud; das Repository liegt unter `/home/builder/work`.

#### Registry und Cluster von `viper up` anlegen lassen (k3d)

Löschen Sie Cluster oft oder haben Sie Cluster mit mehreren Knoten, lädt sonst jeder Knoten das 3-GB-Image einzeln aus dem Internet. Mit `K3D_REGISTRY` legt `viper up` eine Registry von k3d an (falls sie fehlt), schiebt das Image hinein, und die Pods laden es von dort. Es sind Einstellungen wie alle anderen: Sie stehen in `machines/<rechner>.env.local`, in der Gruppendatei `groups/<gruppe>.env` (dann für alle Rechner der Gruppe) oder in `.env.local`, und was in keiner Datei steht, nimmt `viper` aus der Umgebung (`export K3D_REGISTRY=…`). Eine Datei hat Vorrang vor der Umgebung, der Rechner vor der Gruppe vor `.env.local`.

```
PROVIDER=kubernetes
DNS_PROVIDER=none
SERVER_LOCATION=k3d-viper
K8S_IMAGE=ghcr.io/pt9912/viper-machine:v0.33.0
K3D_REGISTRY=viper-registry
K3D_CLUSTER_CREATE=1
K3D_AGENTS=2
```

* `K3D_REGISTRY`: der Name der Registry (der Container heißt `k3d-<name>`). Ohne diese Einstellung ändert sich nichts. Sie gilt nur, wenn `SERVER_LOCATION` ein k3d-Kontext (`k3d-<cluster>`) ist; ein Rechner in einem anderen Cluster ignoriert sie.
* `K3D_CLUSTER_CREATE=1`: `viper up` legt auch den Cluster an, wenn er fehlt, und zwar mit dieser Registry (`k3d cluster create <cluster> --registry-use k3d-<name>:5000`). Ohne die Einstellung bricht `viper up` mit der Anleitung ab, wie Sie den Cluster selbst anlegen. Standard: `0`.
* `K3D_AGENTS`: wie viele Agent-Knoten ein Cluster bekommt, den `viper` anlegt (0 bis 16, Standard `0`: nur ein Server-Knoten).

Dafür muss das Programm `k3d` auf Ihrem Computer installiert sein (`brew install k3d`); der Preflight prüft es und meldet sonst einen Fehler, bevor etwas angelegt wird. Ist die Registry schon da und nutzt der Cluster sie, braucht `viper` `k3d` nicht. `viper up` führt dann der Reihe nach aus: Registry anlegen oder (angehalten) starten, Cluster anlegen (nur mit `K3D_CLUSTER_CREATE=1`), das Image aus Docker nehmen oder mit `docker pull` laden, in die Registry schieben, und die Pods starten mit dem Namen aus der Registry. Alles steht in der Ausgabe von `viper up`. Bei einer Gruppe geschieht das einmal, die Rechner warten aufeinander. `viper preflight` sagt vorher, was passieren wird. Die Knoten holen das Image aus der Registry im lokalen Docker-Netz, ohne Download aus dem Internet; kopiert und entpackt wird auf jedem Knoten weiterhin (bei drei Knoten je 15 bis 29 Sekunden).

**Ihre `kubeconfig` (für `kubectl`):** `viper` liest sie wie `kubectl` (die Dateien aus `KUBECONFIG`, sonst `~/.kube/config`) und nimmt immer den Kontext aus `SERVER_LOCATION`, nie den „aktuellen“. Legt `viper up` den Cluster an, trägt **k3d** den Cluster und den Kontext `k3d-<cluster>` in diese Datei ein; **den aktuellen Kontext stellt es nicht um**, `kubectl` zeigt also weiter auf den Cluster, mit dem Sie vorher gearbeitet haben (`viper` übergibt k3d dafür `--kubeconfig-switch-context=false`). Für `kubectl` auf den neuen Cluster nehmen Sie `kubectl --context k3d-<cluster> …` oder `kubectl config use-context k3d-<cluster>`. Mit `viper down` entfernt k3d den Eintrag wieder (der aktuelle Kontext bleibt, wie er war). Das ist mit k3d 5.9 geprüft. Cluster, die eine ältere Version von `viper` angelegt hat, haben den aktuellen Kontext umgestellt. Eine eigene Datei nur für diese Arbeit nehmen Sie mit `KUBECONFIG=/pfad/zur/datei viper up …` (`viper` und `k3d` nehmen sie beide); das ist nicht erprobt.

Ein Cluster, der schon da ist und ohne die Registry angelegt wurde, bekommt sie nachträglich nicht (k3d kann das nicht). Dann spielt `viper up` das Image mit `k3d image import` in alle Knoten ein, sagt es und nennt die Lösung: den Cluster neu anlegen, am einfachsten mit `K3D_CLUSTER_CREATE=1`. 

**Beim Löschen:** Löschen Sie mit `viper down` den letzten Rechner eines Clusters, den `viper up` angelegt hat, löscht `viper down` den Cluster mit, und die Frage `DELETE` nennt das vorher. Die Registry entscheiden Sie: Danach fragt `viper down` einmal „Delete the k3d registry … too?“, und nur `DELETE` löscht sie, jede andere Antwort (und kein Eingabeende) lässt sie stehen, denn sie hält das Image für künftige Cluster. Wer „behalten“ wählt, dem gehört die Registry ab dann: `viper` fragt nicht wieder, `k3d registry delete k3d-<name>` entfernt sie. Ohne Frage entscheiden `viper down <rechner> --delete-registry` und `--keep-registry`, auch in Skripten und bei `--group`. `viper` entfernt nur, was es selbst angelegt hat (es merkt sich das im Zustandsverzeichnis): einen Cluster oder eine Registry, die schon vorher da waren, rührt es nie an, ebenso keine Registry, die ein anderer Cluster noch nutzt. Sind noch andere Rechner im Cluster, bleibt er bestehen. **Was Sie selbst in einen Cluster einrichten, den `viper` angelegt hat, geht mit ihm verloren**; der Cluster gehört zu den Rechnern, für die er angelegt wurde. Für einen Cluster mit eigenen Inhalten legen Sie ihn selbst an (ohne `K3D_CLUSTER_CREATE`), dann bleibt er immer stehen. Hat jemand Rechner im selben Cluster außerhalb von `viper up` gestartet, zählt `viper` nur seine eigenen (Label `viper-managed`).

#### Hinweise

* `COMPOSE_DOCKER=dind` muss ausdrücklich stehen: Der Docker im Rechner ist ein privilegierter Container, der Root auf Ihrem Computer bedeuten kann, wenn er geknackt wird. Nutzen Sie das nur auf Ihrem eigenen Computer.
* Ist `DOCKER_HOST` in Ihrer Umgebung gesetzt, verweigert `viper` bei Compose die Arbeit (Docker würde den Wert statt des Kontexts nehmen).
* NATS-Gruppen gehen auch lokal: eine Gruppendatei mit mehreren Rechnern und `NATS_ENABLED=true`.
* Daten liegen in Volumes und überstehen einen Neustart des Containers, nicht aber `viper down`.

### Einen Rechner für ein Repository einrichten

#### Voraussetzung

Die erstmalige Einrichtung ist abgeschlossen und die drei Zugangsdaten sind gesetzt.

#### Vorgehen

1. Starten Sie den Rechner und geben Sie die Repository-URL mit an:
   ```bash
   viper up web --repo https://git.example.org/team/web.git
   ```
   Ohne URL wird `REPO_URL` aus `machines/web.env.local` verwendet (siehe Kapitel 4).
2. Warten Sie. `viper up` legt Server, Firewall und DNS-Eintrag an und wartet auf den Abschluss der Servereinrichtung. Das dauert meist einige Minuten, höchstens etwa 20. Dabei werden alle Ubuntu-Updates eingespielt. Brauchen sie einen Neustart (etwa ein neuer Kernel), startet der Server einmal neu, und `viper up` wartet darauf. Nach der Ersteinrichtung schaltet sich cloud-init ab (`/etc/cloud/cloud-init.disabled`), spätere Neustarts laufen ohne cloud-init; `cloud-init status` zeigt dann `disabled`.
3. VS Code öffnet sich automatisch mit dem geklonten Repository, sofern der Befehl `code` vorhanden ist. Mit `viper up <rechner> --no-code` bleibt es zu. Sonst öffnen Sie die Verbindung `hetzner-claude-web` in VS Code Remote-SSH selbst.

#### Ergebnis

Die Meldung `Ready: ssh hetzner-claude-web (work dir: …)` erscheint. Das Repository liegt im Arbeitsverzeichnis unter dem Arbeitsverzeichnis (Standard `/home/builder/work`).

#### Hinweise

* Ein schon vorhandenes Repository wird beim erneuten Aufruf von `viper up` für einen laufenden Server nicht erneut geklont.
* Haben Sie das Repository nur beim Start angegeben (`--repo …`), merkt sich `viper` es für diesen Rechner: Ein späteres `viper up` oder `viper check` ohne `--repo` arbeitet mit demselben Repository. Mit `viper down` wird es vergessen.
* Das Host-Zertifikat des Servers ist sieben Tage gültig. Bei jedem neuen Server wird ein frisches Zertifikat erstellt. Eine manuelle Prüfung des Fingerabdrucks ist nicht nötig.
* Zusätzlich hinterlegt `viper up` den Host-Schlüssel des Servers in `~/.ssh/known_hosts_hetzner_<SERVER_PREFIX>-<rechner>`. `viper`, `ssh` und VS Code vertrauen auf diesem Computer nur diesem Schlüssel. Ein Schlüssel eines früheren Servers wird abgelehnt, auch wenn sein Zertifikat noch gültig ist. Die Verbindung funktioniert auch nach Ablauf des Zertifikats. Fehlt der hinterlegte Schlüssel (Server auf einem anderen Computer angelegt), gilt bei der ersten Verbindung die Host-CA. Danach trägt `viper` den über diese Verbindung gelesenen Schlüssel als hinterlegten Schlüssel ein. Wird der Rechner neu angelegt (neue Server-ID), verwirft es einen alten hinterlegten Schlüssel.
* Schlägt die Installation von Claude Code bei der Einrichtung fehl, holt `viper up` sie beim nächsten Aufruf nach.
* `viper` verbindet sich über die IP-Adresse des Servers. Den DNS-Eintrag brauchen nur Sie für andere Geräte (etwa das iPad). `viper up` wartet deshalb nicht darauf, dass er weltweit sichtbar ist.

### Einen laufenden Rechner prüfen

#### Vorgehen

Führen Sie `viper check <rechner>` aus. Jede Zeile beginnt mit `ok`, `info`, `WARN` oder `FAIL`.

Geprüft werden:

* **DNS:** Der Name `<SERVER_PREFIX>-<rechner>.<DNS_DOMAIN>` löst bei den öffentlichen Resolvern von Cloudflare (1.1.1.1) und Google (8.8.8.8) auf die IP-Adresse des Servers auf. Zusätzlich zeigt der Check, was Ihr Computer auflöst.
* **Zugriff wie ein anderes Gerät:** eine SSH-Verbindung über den DNS-Namen, bei der nur der Host-CA vertraut wird, nicht dem hinterlegten Schlüssel. So sieht das iPad oder ein zweiter Computer den Server, und dabei wird geprüft, dass das Zertifikat für den Namen gilt. Der Check zeigt auch, bis wann das Zertifikat gültig ist.
* **NATS (nur für Rechner einer NATS-Gruppe):** Gruppe, genau ein Server im Zustand `ready`, privates Netzwerk und private Adresse beim Anbieter. Auf dem Rechner: das Serverzertifikat (Ablauf, passende Adresse), die Anmeldung mit dem Schlüssel des Rechners über TLS und, auf dem Server, der Dienst, die Adresse, auf der er lauscht (nur die private), wer registriert ist und ob die Konfiguration noch die Lücke von v0.15.0 enthält (`allow_responses`; Warnung, behoben durch ein weiteres `viper up`). Von Ihrem Computer aus: dass der Port auf der öffentlichen Adresse nicht antwortet.
* **Server:** cloud-init ist fertig. Claude Code ist installiert und, wenn `VIPER_CLAUDE_TOKEN` gesetzt ist, angemeldet und antwortet. `gh` ist installiert und, wenn `VIPER_GH_TOKEN` gesetzt ist, angemeldet. Docker läuft, die Swap ist aktiv (wenn `VIPER_SWAP` gesetzt ist), Homebrew ist installiert und im `PATH` (wenn `VIPER_HOMEBREW=1`), das Repository ist geklont, und die SSH-Härtung wirkt. Dazu kommen freier Speicher und Plattenplatz.

#### Ergebnis

`Check OK.` oder eine Liste der Probleme. Der Check ändert nichts.

#### Hinweise

* Mit gesetztem `VIPER_CLAUDE_TOKEN` oder `VIPER_ANTHROPIC_BASE_URL` stellt der Check Claude Code eine kurze Testanfrage. Sie zählt zu Ihrem Abo bzw. wird beim anderen Anbieter abgerechnet.
* Fehler der Ersteinrichtung liest der Check aus `/var/lib/cloud/data/result.json`, auch wenn cloud-init danach abgeschaltet ist.
* Haben spätere Updates auf dem Server einen Neustart nötig gemacht (Ubuntu meldet beim Login `*** System restart required ***`), warnt der Check. Starten Sie den Server dann mit `viper ssh <rechner> --cmd "sudo reboot"` neu. Laufende tmux-Sitzungen enden dabei, Dateien bleiben erhalten.
* Direkt nach dem Anlegen kann die DNS-Prüfung für einige Minuten fehlschlagen, bis die Resolver den neuen Eintrag kennen.
* Der lokale Resolver ist der Ihres Computers. Was er auflöst, prüfen Sie zusätzlich mit `getent hosts <name>` oder `ping <name>`.

### Passenden Server-Typ suchen

`viper find` sucht in den Preislisten Ihrer Cloud-Anbieter die günstigsten Server-Typen, die mindestens das haben, was Sie angeben, und stellt sie **vergleichbar** nebeneinander. Es liest nur und legt nichts an. Die Anbieter werden gleichzeitig abgefragt.

#### Voraussetzung

* `.env.local` ist angelegt (`viper setup`).
* Kein Anbieter braucht für `viper find` ein Konto: Alle Preise stammen aus öffentlichen Quellen. Zugangsdaten, die Sie schon haben, machen die Angaben nur genauer (siehe die Tabelle). Kann eine Quelle nicht gelesen werden, überspringt `viper find` den Anbieter mit einem Hinweis, die anderen laufen weiter.

| Anbieter | Für die Preisabfrage nötig |
|---|---|
| Hetzner | nichts: `viper find` liest die öffentlichen Seiten von hetzner.com (Preise ohne Mehrwertsteuer). Ist `HCLOUD_TOKEN` gesetzt, fragt es zuerst Ihr Projekt ab: Das gibt die Preise Ihres Projekts mit dessen Mehrwertsteuersatz und nur die Typen, die Sie dort bestellen können. Klappt das nicht, nimmt es die Seiten und sagt es; deren Preise sind ohne Mehrwertsteuer. |
| AWS | nichts: `viper find` liest die öffentliche Preisliste von AWS, ohne Konto und ohne Anmeldung |
| Azure | nichts: Preise und VM-Größen sind öffentlich. Mit `AZURE_SUBSCRIPTION_ID` und `az login` fragt `viper find` die Größen genauer bei Ihrer Subscription ab. |
| Google Cloud | nichts: `viper find` liest die öffentlichen Preisseiten von Google. Ist `GCP_PROJECT` gesetzt, fragt es zuerst Ihr Projekt ab (dafür `gcloud auth application-default login` und die aktivierte Cloud Billing API); klappt das nicht, nimmt es die Preisseiten und sagt es. |

#### Vorgehen

`viper find --cpu 4 --ram 16 --disk 80`

* `--cpu N`, `--ram GB`, `--disk GB`: Mindestwerte; mindestens einer ist Pflicht.
* `--arch x86` oder `--arch arm`: nur diese Prozessor-Architektur (Standard: beide).
* `--location ORT`: nur dieser Standort, etwa `fsn1` (Hetzner), `eu-central-1` (AWS), `germanywestcentral` (Azure) oder `europe-west3-b` (Google Cloud). Standortnamen unterscheiden sich je Anbieter, deshalb braucht `--location` immer `--provider`.
* `--provider NAME`: nur dieser Anbieter (`hetzner`, `aws`, `azure`, `gcp`).
* `--vat PROZENT`: Mehrwertsteuersatz für alle Preise; `--vat 0` zeigt Nettopreise. Ohne Angabe gilt der Satz Ihres Hetzner-Projekts (nur mit `HCLOUD_TOKEN`, bei Ihnen 19 %), sonst keine Steuer: Die Legende der Tabelle sagt, was gilt. Wollen Sie ohne Token Preise mit Steuer sehen, geben Sie den Satz an, etwa `--vat 19`.

Ohne `--location` sucht Hetzner an allen Standorten. AWS, Azure und Google Cloud preisen eine Region nach der anderen: Sie suchen im `SERVER_LOCATION` Ihrer `.env.local`, wenn `PROVIDER` dort der Anbieter ist, sonst in der Standardregion des Anbieters.

#### Ergebnis

Eine Tabelle für alle Anbieter, **nach Monatspreis sortiert**. Je Anbieter stehen die drei günstigsten Typen darin, mit Standort, Kernen, RAM, Platte und Preis je Stunde und je Monat. Jeder Typ erscheint an dem Standort, an dem er am günstigsten ist. Darunter erklärt die Legende, was die Preise sind, und die Zeilen `PROVIDER=… SERVER_TYPE=… SERVER_LOCATION=…` nennen den günstigsten Typ je Anbieter zum Übernehmen in Ihre `machines/<rechner>.env.local`. Passt kein Typ, endet `viper find` mit Code 1.

Damit die Preise vergleichbar sind, rechnet `viper find` sie auf **eine Basis** um:

* **Währung:** alles in Euro. AWS bietet nur US-Dollar; `viper find` rechnet sie mit dem Referenzkurs der Europäischen Zentralbank um (öffentlich, ohne Konto). Die Legende nennt Kurs und Tag. Ist kein Kurs zu bekommen, bleibt die Währung stehen, und die Legende sagt es: Diese Zeilen stehen dann nach allen Euro-Zeilen und sind nicht mit ihnen vergleichbar. Ein gemerkter Kurs gilt höchstens sieben Tage, wenn die Datei der EZB nicht zu lesen ist.
* **Mehrwertsteuer:** für alle Anbieter dieselbe (`--vat`). Hetzner nennt seine Preise über das Projekt mit Steuer, die anderen ohne; `viper find` rechnet beides auf denselben Satz um.
* **IPv4-Adresse:** Jeder Rechner bekommt eine öffentliche IPv4-Adresse, und alle Anbieter berechnen sie. Ihre Gebühr steckt in den Preisen (Hetzner 0,50 € im Monat, AWS und Google Cloud 0,005 US-Dollar je Stunde, Azure rund 0,0044 € je Stunde für die Standard-Adresse). Die Legende nennt die Anbieter, bei denen sie fehlt, falls eine Quelle sie nicht liefert.

Bei AWS, Azure und Google Cloud wird die Systemplatte getrennt vom Typ gewählt und berechnet. Ihre Größe steht mit `*` in der Spalte `DISK GB` (`--disk`, ohne Angabe die Standardgröße von `viper`), ihr Preis ist in den Preisen enthalten, und die Zeile für den Anbieter nennt die passende Einstellung, etwa `AWS_ROOT_GB=80`. Bei Hetzner gehört die Platte zum Typ: `--disk` ist dort eine Mindestgröße.

#### Hinweise

* Die Preise sind Listenpreise mit der IPv4-Adresse. Nicht enthalten sind Rabatte und Datenverkehr. Die Hetzner Console zeigt ihre Preise mit IPv4 und Mehrwertsteuer; mit `--vat 19` (oder `HCLOUD_TOKEN`) stimmen die Zahlen mit ihr überein.
* Hetzner ohne Token: Die Preise stammen aus einer Datei, die die Seiten von hetzner.com lesen, die Ausstattung aus dem Text der Seiten. Typen, die die Seiten als `not available` führen, fehlen (zurzeit die Familien `CX` und `CAX`). Falkenstein (`fsn1`) hat den Preis von Nürnberg, weil beide in Deutschland liegen und die Datei nur einen nennt. Ob ein Typ an einem Standort gerade bestellbar ist, prüft nur der Weg mit dem Token.
* Der Monatspreis rechnet mit 730 Stunden.
* Hat `viper find` einen Preis nicht frisch lesen können, nimmt es den gemerkten Stand, aber höchstens sieben Tage alt; ist er älter, meldet es den Anbieter als nicht lesbar.
* **Cache:** Die Preislisten sind groß (AWS je Region mehrere hundert MB, Azure und Google Cloud rund 60 MB, Hetzner nur wenige MB). `viper find` liest sie höchstens einmal am Tag und merkt sich nur das Nötige (wenige KB je Region) im Cache-Verzeichnis, unter Linux `~/.cache/viper/`. Der erste Lauf einer Region dauert deshalb etwas länger (`aws: reading the price list …`), die folgenden sind sofort fertig. Lässt sich eine Liste später nicht lesen, gilt der alte Stand weiter. Zum Erneuern vor Ablauf löschen Sie die Dateien. Bricht ein Download ab (die AWS-Datei ist groß), versucht `viper find` es dreimal und nennt dann einen Netzwerkfehler (Proxy oder VPN kommen als Ursache in Frage).
* AWS: Bare-Metal-Typen (`*.metal`) fehlen.
* Azure: Ohne Subscription stammen Kerne und RAM aus den Daten des Preisrechners auf der Azure-Website. Das ist keine dokumentierte Schnittstelle; ändert Microsoft sie, fehlen nur diese Angaben, bis `viper` angepasst ist.
* Google Cloud: Die Preisseiten (Allzweck, Compute-, Arbeitsspeicher- und Speicheroptimiert) und die Seite der Platten sind Webseiten, keine Schnittstelle; `viper find` liest die Tabellen daraus (zusammen rund 60 MB, einmal am Tag, Preise je Region in Dollar). Die Gebühr für die IPv4-Adresse steht auf der Netzwerk-Preisseite. Ändert Google eine der Seiten, bricht `viper find` für Google Cloud mit `no prices found …` ab, und es erscheinen keine falschen oder unvollständigen Preise. Bei Tabellen mit Rabattspalten gilt der höchste Preis der Zeile, der Preis ohne Rabatt. Geteilte Kerne (`e2-micro`, `e2-small`, `e2-medium`), Typen mit lokaler SSD und Beschleuniger-Typen (GPU) fehlen. Ob ein Typ in Ihrer Zone angeboten wird, prüft `viper find` nicht. Bei den neuen Serien mit Hyperdisk kommen Leistungsaufschläge der Platte nicht dazu. Über das Projekt (Cloud Billing) setzt sich der Preis aus den Preisen für Kerne und Arbeitsspeicher der Typenfamilie zusammen; Typen mit GPU fehlen dort, weil die GPU getrennt berechnet wird.  Auch dort liest `viper find` die Preise in Dollar und rechnet sie mit dem Kurs der EZB um; die Zahlen sind mit und ohne Projekt dieselben.
* Der Weg über das eigene Konto (Azure-Subscription, Google-Projekt) ist für `viper find` nicht gegen ein echtes Konto belegt. Die öffentlichen Quellen (AWS, Azure, Google Cloud) sind gegen die echten Dateien und Seiten geprüft.

### Protokolle ansehen

`viper up` (`viper up`), `viper down` (`viper down`), `viper check` (`viper check`) und ein eigenständig aufgerufenes `viper preflight` schreiben zwei Protokolle in `~/.local/state/viper/` (Rechte `600`, ohne Tokens):

* `events.log`: eine Zeile pro Ereignis mit Zeit (lokale Zeit mit Abstand zu UTC, etwa `2026-10-21T15:12:03+02:00`), Server, Aktion und Ergebnis, etwa `server created id=…`, `ready`, `deleted`, `cancelled (unsaved work)`, `interrupted` (Strg-C, geschlossenes Terminal) oder `FAILED (exit 1; …)`. Daraus sehen Sie, welche Server wann liefen.
* `logs/<SERVER_PREFIX>-<rechner>.log`: die vollständige Ausgabe jedes Laufs, Läufe getrennt durch eine Zeile `===== <Zeit> <aktion> <server>`. Am Ende jedes `viper up` steht dort unter `== installed on the server ==`, was auf dem Server installiert ist: Betriebssystem und Kernel, die apt-Pakete aus `cloud-init.yaml` mit Version, Claude Code, `gh`, Docker sowie Homebrew mit Taps, vertrauten Einträgen und den angeforderten Formeln samt Version. Das Protokoll von Homebrew selbst (`~/.config/viper/brew-packages.log`) bleibt auf dem Server.

#### Vorgehen

* Alle Ereignisse (die letzten 50): `viper log`
* Ereignisse eines Rechners: `viper log <rechner>`
* Vollständige Ausgabe eines Rechners: `viper log <rechner> --full`

`viper log` braucht keine Tokens. Eine Repo-URL mit Zugangsdaten (`https://user:token@…`) lehnt der Preflight ab, damit das Token weder in die Protokolle noch auf den Server gelangt. Dasselbe zeigt die Seite **Protokolle** von `viper ui` (siehe „Die Oberfläche im Browser“). Die Protokolle werden nicht automatisch gekürzt; löschen Sie sie bei Bedarf von Hand. Protokolle auf dem Server (cloud-init, Homebrew) verschwinden dagegen mit dem Server.

### Die Oberfläche im Browser

`viper ui` startet auf Ihrem Rechner eine kleine Weboberfläche und öffnet den Browser. Sie zeigt die Rechner und Gruppen aus den Einstellungsdateien, lässt je Rechner `viper check` laufen, zeigt die Protokolle und die Einstellungen, startet `preflight`, `up` und `down` für einen Rechner oder eine Gruppe und spricht mit den NATS-Gruppen (wer ist da, mitlesen, Nachrichten und Dateien senden und holen). Was sie tut, tut `viper` selbst: dieselben Befehle, Sperren, Rückfragen und Protokolle wie am Terminal.

#### Voraussetzung

* Ein Verzeichnis mit Einstellungen (`.env.local`, `machines/`, `groups/`) wie für die Befehle; es ist das aktuelle Verzeichnis oder `--dir`.
* Ein Browser auf demselben Computer. Die Oberfläche ist nur von diesem Computer aus erreichbar.
* Für **Check** und die NATS-Seite `ssh`, wie für die Befehle selbst.

#### Vorgehen

1. Starten Sie `viper ui` im Verzeichnis mit den Einstellungen. Der Browser öffnet sich; die Adresse (`http://127.0.0.1:<port>/`) steht auch im Terminal. `Strg-C` im Terminal beendet die Oberfläche. Öffnet sich der Browser nicht, meldet `viper ui` das und nennt die Adresse mit dem Schlüssel (eine Minute gültig); mit `--no-open` startet es den Browser nicht, und der Schlüssel gilt zehn Minuten.
2. **Übersicht:** Der **Name** eines Rechners oder einer Gruppe ist ein Knopf zu seinen Einstellungen („Einstellungen von web“): Ohne `--edit` öffnet er die Seite „Einstellungen“ beim Block der Datei (`machines/<name>.env.local` oder `groups/<name>.env`), mit `--edit` die Seite „Ändern“ mit dieser Datei gewählt. Hat ein Rechner keine eigene Datei (nur eine Gruppe nennt ihn), führt der Knopf zur Datei der Gruppe oder zu `.env.local`, und eine Zeile sagt es. Links stehen die Seiten (bei schmalem Fenster als Leiste oben). Die Übersicht ist nach **Gruppen** gegliedert: Jede Gruppe aus `groups/*.env` ist ein Block mit einer Kopfzeile (Name, **Zustand der Gruppe** und die Knöpfe der Gruppe), darunter ihre Rechner; Rechner ohne Gruppe stehen zuletzt unter „Ohne Gruppe“, ein Rechner in mehreren Gruppen unter jeder. Je Rechner gibt es Anbieter, Typ, Ort und NATS-Gruppe. Die Spalte „Zustand“ zeigt eine **Lampe mit Wort**, was dieser Computer weiß: „gestartet“ (`viper up` ist durchgelaufen, der Rechner ist eingerichtet), „angelegt“ (der Server ist da, `viper up` ist aber noch nicht fertig oder abgebrochen) oder „nicht gestartet“; ein farbiger Streifen am Rand der Zeile wiederholt es. Der Zustand der Gruppe ist „gestartet“, wenn alle Rechner es sind, „nicht gestartet“, wenn keiner angelegt ist, sonst „teilweise“ mit den Zahlen („1 gestartet, 1 angelegt“). Die wahrscheinlich nächste Aktion ist gefüllt (ein Rechner, der nicht da ist: **Up**; einer, der da ist: **Prüfen**), **Down** ist rot. Ob der Rechner wirklich läuft, beantwortet **Prüfen**: Das ist `viper check`, mit dessen Ausgabe; „in Ordnung“ heißt Exit-Status 0.
   **Filtern und sortieren:** Über der Tabelle filtert das Feld „Filter“ nach Wörtern (alle Wörter müssen zu Name, Gruppe, Anbieter, Typ, Ort, NATS-Gruppe oder Zustand eines Rechners passen; passt der Name einer Gruppe, zeigt sie alle ihre Rechner), die Auswahl „Zustand“ nach dem Zustand; „Filter löschen“ nimmt beides zurück, und daneben steht, wie viele Rechner passen. Die Überschriften der Spalten Rechner, Anbieter, Typ, Ort, NATS und Zustand sortieren die Rechner **innerhalb jeder Gruppe** (ein zweiter Klick kehrt um, ein dritter hebt die Sortierung auf; nach Zustand: gestartet, angelegt, nicht gestartet); die Gruppen bleiben nach Namen geordnet. Der Zustand einer Gruppe zählt immer alle ihre Rechner, auch wenn der Filter nur einige zeigt. Filter und Sortierung bleiben, bis Sie die Seite neu laden.
3. **Aktionen:** (Das Menü **Optionen für Läufe** (rechts in der Zeile des Filters; Esc oder ein Klick daneben schließt es) fügt den Läufen Optionen an, die `viper` kennt: **Rechner einer Gruppe gleichzeitig** (`--parallel`, 1 bis 64, leer: 8; nur für die Knöpfe einer Gruppe), bei Down **auch löschen, wenn der Rechner nicht erreichbar ist oder noch Mitglieder hat** (`--force`; gilt nur für das nächste Down, Kästchen und Registry-Wahl schalten sich beim Start zurück; ist der Rechner erreichbar, prüft `viper` trotzdem auf ungesicherte Arbeit) und **die Registry eines k3d-Clusters** (fragen, löschen, behalten). Das Kästchen **Beim Start eines Laufs auf dieser Seite bleiben** (im selben Menü, aus, bleibt im Browser erhalten) hält die Oberfläche beim Start eines Laufs auf der Seite, auf der Sie sind, statt auf die Seite „Läufe“ zu wechseln; eine Zeile oben auf der Seite sagt, was gestartet wurde („Gestartet: terminal web“), und der Lauf steht auf der Seite „Läufe“. Auch bei geschlossenem Menü steht hinter „Optionen für Läufe“, wie viele Optionen gesetzt sind („(2 gesetzt)“). Die erste Zeile des Laufs zeigt den Befehl mit den Optionen. Die Rückfragen von `viper down` bleiben.) **Gruppe prüfen** in der Kopfzeile einer Gruppe startet `viper check --group` für alle Rechner der Gruppe als Lauf (die Ausgabe hat eine Zeile je Rechner und am Ende die Tabelle mit dem Ergebnis); das geht auch mit `--readonly`: Prüfen ändert keine Einstellung und legt keinen Rechner an (es schreibt wie am Terminal Protokolle und `events.log`). (Solange ein Lauf einen Rechner hält, zeigt die Übersicht in der Spalte „Zustand“ das Wort „fährt hoch …“, „wird abgebaut …“, „wird vorgeprüft …“ oder „wird geprüft …“ mit schlagender Lampe, bei einer Gruppe mit der Zahl der betroffenen Rechner, und die Knöpfe dieses Rechners sind aus (nur in der Gruppe, deren Lauf ihn hält; steht er in mehreren Gruppen, bleiben die anderen Zeilen unverändert); der Zustand in Klammern folgt den Marken dieses Computers und ändert sich, sobald der Server da ist.) **Vorprüfung**, **Up** und **Down** je Rechner und je Gruppe starten `viper preflight`, `viper up` und `viper down` als eigenen Prozess. Das Feld „Repository-URL“ gilt für Up und Vorprüfung (`--repo`; eine URL mit Zugangsdaten lehnt die Oberfläche ab). Die Seite **Läufe** zeigt die Ausgabe, solange der Lauf geht. **Terminal** (nur bei einem gestarteten Rechner, nicht mit `--readonly`) startet `viper terminal`: Auf **diesem Computer** öffnet sich ein Terminalfenster mit der tmux-Sitzung `claude` des Rechners (dieselbe wie in den Terminals von VS Code; zwei Fenster zeigen dieselbe Sitzung). Das Fenster nimmt den Fokus: was Sie danach tippen, landet auf dem Rechner. Ohne Desktop meldet der Lauf den Fehler. **VS Code** (ebenso) startet `viper vscode`: VS Code öffnet sich mit Remote-SSH auf **diesem Computer** (dort muss der Befehl `code` stehen), und es richtet auf dem Rechner seinen Server und Erweiterungen ein; ohne Desktop meldet der Lauf den Fehler. Ein neuer Tab findet laufende Läufe wieder, und das Schließen eines Tabs beendet nichts.
4. **Rückfragen:** Fragt `viper` etwas (zum Beispiel „Type DELETE to continue“), erscheint unter der Ausgabe ein Eingabefeld. Sie tippen das Wort selbst; es gibt keinen Knopf, der für Sie bestätigt.
5. **Abbrechen:** Der Knopf wirkt wie Strg-C am Terminal: `viper` bricht ab und räumt auf, was es kann. Es macht nichts rückgängig; ein halb angelegter Server kann bleiben. Das Beenden kann einige Sekunden dauern („Wird beendet …“); nach 20 Sekunden beendet die Oberfläche den Prozess hart.
   **Läufe entfernen:** Die Liste der Läufe zeigt die laufenden und bis zu 20 beendete. Mit **Entfernen** an einer beendeten Zeile oder **Beendete Läufe entfernen** (alle beendeten, auch die, die eine zweite offene Seite zeigt) nehmen Sie sie aus der Liste (mit ihrer Ausgabe; die Protokolle und `events.log` bleiben); ein laufender Lauf bleibt und wird erst nach dem Abbrechen entfernt. Entfernen ist auch mit `--readonly` möglich.
6. **NATS:** Die Seite zeigt die Gruppen aus `groups/*.env`, deren Rechner `NATS_ENABLED=true` haben. Die Zeile darunter sagt, wie `viper nats` spricht: **als Betreiber**, wenn dieser Computer den Schlüssel der Gruppe hat (`viper nats init <gruppe>`), sonst **als Rechner der Gruppe** über ssh („Sprechen als“ wählt den Rechner). Jede Schaltfläche startet `viper nats …` als Lauf:
    * **Als Betreiber anmelden** (`viper nats init <gruppe>`; nur sichtbar, solange dieser Computer keinen Schlüssel hat): legt den Schlüssel des Betreibers auf diesem Computer an und meldet den Betreiber am NATS-Server der Gruppe an (über einen SSH-Tunnel; der Server der Gruppe muss laufen). Danach sendet `viper nats` als Betreiber. Der Betreiber kann alles in der Gruppe lesen, und die Agenten sehen ihn als Absender. **Betreiber abmelden** (`viper nats revoke <gruppe>`; sichtbar, solange der Schlüssel da ist) entfernt ihn am Server und löscht den Schlüssel; der Knopf fragt vorher ein zweites Mal.
    * **Peers** und **Themen**: wer da ist und welche Themen die Rechner abonniert haben. „Warten (Sekunden)“ ist die Zeit, die `viper nats` auf Antworten wartet.
    * **Mitlesen** (ein eigener Bereich auf der Seite): **Mitlesen** startet `viper nats listen`, die Nachrichten erscheinen darunter im Bereich (die Seite bleibt die Seite von NATS); ein Thema begrenzt (leer: die Themen der Einstellungen), „alles in der Gruppe“ zeigt alles. **Anhalten** beendet es wie Strg-C, **Leeren** leert den Bereich. Der Bereich scrollt mit, solange Sie am Ende lesen (lesen Sie weiter oben, bleibt er stehen) und behält die letzten 2000 Zeilen. Der Lauf steht auch in der Liste der Läufe; öffnen Sie die Seite von NATS neu (oder laden Sie sie neu), hängt der Bereich sich an einen Lauf dieser Gruppe, der noch geht. Hat eine Nachricht eine Datei, steht dort **Diese Datei holen**; der Knopf trägt ID und Prüfsumme in das Formular „Datei holen“ ein.
    * **Senden:** Nachricht an einen Rechner, Anfrage (wartet „Auf die Antwort warten“ Sekunden, Standard 30) oder Rundruf an ein Thema; der Text bis 16 KiB. Eine Datei (PDF, Bild oder ZIP) geht nur an eine Gruppe mit Objektspeicher (`NATS_OBJECTS=true`; ohne ihn ist das Feld aus) und darf nicht größer sein als `NATS_OBJECTS_FILE_MAX` der Gruppe, in der Oberfläche höchstens 64 MiB; höchstens acht Dateien warten zugleich auf das Senden. Die weiteren Prüfungen sind die von `viper nats`. Text und Datei lassen sich auch **einfügen und hineinziehen**: Eine Datei (PDF, Bild oder ZIP) ziehen Sie auf das Formular, oder Sie fügen sie mit `Strg+V` ein (auch ein Bildschirmfoto aus der Zwischenablage, das als `pasted.png` mitgeht). Eine Textdatei (`.txt`, `.md`, `.log`, `.json`, `.csv`) wird zum Text der Nachricht, ebenso ein Text, den Sie hineinziehen oder (außerhalb eines Feldes) einfügen. Zu einer Nachricht gehört eine Datei, weitere lässt die Seite weg und sagt es; eine zu große oder unbrauchbare Datei nimmt sie nicht. „Datei entfernen“ nimmt die gewählte Datei wieder heraus. Der Text steht nie in der Befehlszeile eines Programms. Nach dem Senden leert die Seite Text und Datei. Eine Datei, die niemand sendet (etwa weil die Nachricht abgelehnt wurde), löscht die Oberfläche selbst, spätestens nach 30 Minuten. Die Warnung, dass der Absender in `NATS_MCP_PUSH_FROM` eines Empfängers steht, erscheint als Fehler des Laufs; „trotzdem senden“ entspricht `--yes`.
    * **Datei holen** (`viper nats fetch`, nur mit dem Schlüssel des Betreibers): Nach dem Lauf erscheint **Datei herunterladen**. Die Datei stammt von einem anderen Agenten: nicht ausführen; die Seite lädt sie immer als Download, nie als Seite.
7. **Finden** (`viper find`): Die Seite sucht in den Preislisten der Anbieter die günstigsten Server-Typen mit mindestens so viel CPU, RAM und Platte. Sie füllen CPUs, RAM (GB) und Platte (GB) aus (mindestens eines), wählen Architektur und Anbieter (hetzner, aws, azure, gcp), den Standort (`fsn1`, `eu-central-1`; Kleinbuchstaben; nur mit einem Anbieter) und die Mehrwertsteuer in Prozent (leer: der Satz, den ein Anbieter meldet, sonst 0). **Suchen** startet `viper find …` als Lauf; die Ausgabe (die Tabelle von `viper find`, englisch) erscheint im Bereich darunter, die Seite bleibt die Seite. **Abbrechen** beendet die Suche wie Strg-C. Es läuft höchstens eine Suche zugleich; sie steht auch in der Liste der Läufe, und nach dem Neuladen hängt sich der Bereich an eine laufende Suche. Endet `viper find` mit Exit-Status 1 (kein Treffer oder ein Fehler wie die fehlende `.env.local`), zeigt die Seite „Kein Server-Typ gefunden, oder die Suche ist fehlgeschlagen“; die Ursache steht in der Ausgabe. `viper find` braucht eine `.env.local` im Einstellungsverzeichnis, legt Preislisten im Cache-Verzeichnis ab (die erste Suche lädt große Listen) und fragt Azure und GCP mit den vorhandenen Zugangsdaten ab; es geht auch mit `--readonly` (es ändert keine Einstellung und legt keinen Rechner an).
8. **Image** (`viper image build`): Die Seite baut das Image der Rechner für die Anbieter `compose` und `kubernetes` mit Docker. Sie tippen, wenn Sie wollen, den **Namen des Images** (`name:tag` in Kleinbuchstaben, leer: `viper-machine:<Version>`) und einen **k3d-Cluster**, in den das Image eingelegt wird. **Bauen** startet `viper image build …` als Lauf; die Ausgabe steht im Bereich darunter, **Abbrechen** beendet den Lauf. Das erste Bauen dauert mehrere Minuten. Es läuft höchstens ein Bau zugleich, er steht auch in der Liste der Läufe, und nach dem Neuladen hängt sich der Bereich an einen laufenden Bau. Docker (bei einem Cluster auch `k3d`) muss auf diesem Computer installiert sein. Mit `--readonly` bietet die Seite das Bauen nicht an.
9. **Befehl** (nur mit `--edit`): Die Seite führt **einen** Befehl mit `viper ssh --cmd` auf einem Rechner oder auf jedem Rechner einer Gruppe aus, als Ihr Benutzer auf dem Rechner (mit allem, was er dort darf; auch auf Rechnern mit `compose` oder `kubernetes`). Sie wählen den Rechner oder die Gruppe, tippen den Befehl (eine Zeile, bis 4096 Byte) und das Wort `run` zur Bestätigung (eine Bremse gegen einen Fehlklick); **Befehl ausführen** startet den Lauf, dessen Ausgabe auf der Seite „Läufe“ steht (die erste Zeile zeigt den Befehl). Ein Befehl darf **nicht mit einem Bindestrich beginnen** (`ssh` läse ihn als Option; schreiben Sie `exec -- …` oder `sh -c '…'`). Bei einer Gruppe gilt `--parallel` aus „Optionen für Läufe“, und der Status des Befehls geht nicht durch: Exit 1 heißt, mindestens ein Rechner ist fehlgeschlagen. Der Lauf bekommt **keine Eingabe** (die Standardeingabe ist geschlossen), stellt keine Rückfrage und hat **kein Zeitlimit**: Ein Befehl, der lange läuft, hält den Rechner (ein `viper down` darunter geht nicht) und die Zugangsdaten sind gesperrt, bis Sie ihn mit **Abbrechen** beenden; starten Sie ihn mit `timeout 60 …`. Schreiben Sie **keine Geheimnisse** in den Befehl: Er steht im Lauf, solange `viper ui` läuft, und in der Prozessliste dieses Computers, und `viper` schwärzt nur die Zugangsdaten Ihrer Einstellungen, nicht Werte, die nur auf dem Rechner liegen (`env`, `cat .env.local`). In `events.log` steht nur eine Zeile mit dem Rechner und der Länge des Befehls. Mit `--readonly` gibt es die Seite nicht.
10. **Einstellungen:** Jede Datei ist ein Block zum Auf- und Zuklappen mit ihrem Namen und der Zahl ihrer Einstellungen; zu Beginn ist `.env.local` offen, die anderen sind zu, und die Einstellungen einer Datei werden erst geladen, wenn Sie sie öffnen (200 auf einmal, **weitere anzeigen** holt die nächsten). Das **Suchfeld** oben findet Einstellungen in allen Dateien nach einem Teil ihres Namens (höchstens 500 Treffer, mit der Datei); der Wert einer Einstellung wird bei 512 Zeichen gekürzt, eine Datei über 1 MiB wird nicht aufgelistet (die Zeile sagt es), und die Liste nennt höchstens 5000 Dateien. Die Seite zeigt die Namen der Einstellungen jeder Datei im Einstellungsverzeichnis (`.env.local`, `groups/*.env`, `machines/*.env.local`) und die Namen bekannter Einstellungen, die nur die Umgebung setzt. Den **Wert** zeigt sie nur für Einstellungen einer festen Liste, die keine Zugangsdaten sind und nichts Privates enthalten (zum Beispiel `PROVIDER`, `SERVER_TYPE`, `SERVER_LOCATION`, `GROUP_MACHINES`, `REPO_URL`, `VIPER_BREW_PACKAGES`, `VIPER_BREW_TAPS`, `VIPER_BREW_TRUST`, `VIPER_BREWFILE`, `VIPER_POST_CLONE_ON_ERROR`, `VIPER_POST_CLONE`, die Einstellungen der NATS-Gruppe wie `NATS_ENABLED`, `NATS_GROUP`, `NATS_PORT`, `NATS_OBJECTS*` und `NATS_MCP_TOPICS`, dazu `K3D_*`); alles andere steht als „gesetzt“, auch ein Token, ein Passwort und der Pfad eines Schlüssels. Eine `REPO_URL` (oder ein Tap in `VIPER_BREW_TAPS`) mit Zugangsdaten (`https://benutzer:token@…`) zeigt die Seite ebenfalls nur als „gesetzt“, ebenso einen Wert über mehrere Zeilen (ein mehrzeiliges `VIPER_POST_CLONE`: lesen Sie ihn in der Datei); die Vorprüfung lehnt eine solche URL ab (nehmen Sie `VIPER_GH_TOKEN` oder `GIT_DEPLOY_KEY_FILE`). Werte, die ein Programm erst ausrechnen müsste (`$VAR`), zeigt sie nicht, aus der Umgebung nur die Namen. Ändern lässt sich hier nichts; mit `--edit` gibt es dafür die Seiten **Ändern**, **Zugangsdaten** und **Anlegen** (siehe unten).
11. **Anlegen** (nur mit `--edit`): Die Seite legt eine **neue** Datei mit Einstellungen an: einen Rechner (`machines/NAME.env.local`), eine Gruppe (`groups/NAME.env`) oder, solange es sie nicht gibt, die Grunddatei `.env.local`. Sie wählen, was, tippen den Namen (Kleinbuchstaben, Ziffern, Bindestriche) und füllen die Felder aus; leere Felder bekommen ihren Standard und stehen nicht in der Datei. **Vorschau** zeigt den Text der Datei, was `viper up` noch verlangt (zum Beispiel die Zugangsdaten des Anbieters, die diese Seite nicht abfragt: sie gehören in `.env.local` oder die Umgebung) und was nicht geht. **Datei anlegen** ist erst nach einer Vorschau ohne Beanstandung möglich und nur, solange sich das Formular nicht geändert hat. Beanstandet wird: ein Name, den `viper` nicht liest oder den Windows für ein Gerät hält (`con`, `nul`, `com1` …) oder der mit `example` beginnt; eine Datei, die es schon gibt (auch in anderer Schreibweise; sie wird nie ersetzt oder geändert); ein Verzeichnis `machines` oder `groups`, das ein Link ist; ein Wert mit Zeilenumbruch oder Steuerzeichen; eine Gruppe, die einen Rechner nennt, den es nicht gibt, oder die einen anderen Rechner **in zwei Gruppen** brächte oder sonst etwas zum Fehler machte, das bisher geladen wurde („Würde brechen: …“). Ein Container-Anbieter (`compose`, `kubernetes`) bekommt `DNS_PROVIDER='none'` von selbst. Die Datei entsteht mit den Rechten `0600`; das Anlegen steht als Zeile in `events.log` (nur die Dateinamen, nie Werte).
   **Duplizieren:** Mit `--edit` hat jeder Rechner und jede Gruppe in der Übersicht den Knopf **Duplizieren** („Duplizieren von web“). Er öffnet **Anlegen**, füllt das Formular mit dem, was die Datei an Einstellungen hat, die das Formular kennt (Anbieter, Typ, Ort, Image, DNS-Art; bei einer Gruppe NATS), setzt einen freien Namen (`web-2`) und den Cursor ins Namensfeld. Die Zeile darunter nennt, was **nicht** übernommen wurde (nur die Namen, zum Beispiel `HCLOUD_TOKEN`, `REPO_URL`, `VIPER_POST_CLONE`). **Zugangsdaten werden nie kopiert**: Setzen Sie sie danach auf der Seite „Zugangsdaten“. Bei einer Gruppe bleibt die Auswahl der Rechner leer, denn ein Rechner in zwei Gruppen bricht (die Vorschau sagt es); wählen Sie die Rechner der Kopie. Danach gilt alles wie beim Anlegen: Vorschau, Prüfung aller, nie eine vorhandene Datei ersetzen.
12. **Ändern** (nur mit `--edit`): Die Seite ändert oder entfernt **eine** Einstellung in einer vorhandenen Datei (`.env.local`, `groups/*.env`, `machines/*.env.local`). Sie wählen die Datei; die Tabelle zeigt ihre Einstellungen, die diese Seite ändern kann, mit dem Wert (nur bei Einstellungen der Liste von „Einstellungen“) und den Knöpfen **Ändern** und **Entfernen** (fragt in der Taste noch einmal, mit dem Namen). Darunter wählen Sie eine Einstellung und tippen den Wert; die Eingabe folgt der Art der Einstellung (Auswahl, Zahl mit Grenzen, Text), und ein kurzer Hilfetext in der Sprache der Seite sagt, was die Einstellung bewirkt, was der Standard ist und wann eine Änderung gilt. Die Tabelle zeigt die ersten 1000 Einstellungen der Datei (die Zeile darüber sagt, wenn es mehr sind); ist ein Wert länger als 512 Zeichen, kommt er nicht ins Feld (die Seite kennt nur seinen Anfang): tippen Sie dann den neuen Wert. Angeboten werden nur bekannte Einstellungen (Anbieter, Typ, Ort, Image, DNS, Gruppen und NATS, Homebrew, Repository …); **Zugangsdaten** gehören auf die Seite „Zugangsdaten“, und `VIPER_POST_CLONE` (Shell-Befehle auf dem Server, auch über mehrere Zeilen) ändern Sie hier in einem mehrzeiligen Feld: **Ändern** in der Zeile liest die vorhandenen Befehle in das Feld; geschrieben werden sie in einfachen Anführungszeichen (`$VARIABLEN` kommen unverändert auf dem Server an, ein `'` im Text wird richtig geschrieben). Die Vorschau zeigt den **ganzen Befehlstext** und verlangt zum Setzen **und** zum Entfernen, den Namen des Rechners (oder der Gruppe, oder `.env.local`) zu tippen: Die Befehle laufen als Ihr Benutzer auf dem Server, mit allem, was er dort darf. Der Text steht auch in der Sicherung; **schreiben Sie keine Tokens hinein** (nehmen Sie `VIPER_GH_TOKEN`), denn nur bekannte Zugangsdaten werden geschwärzt. **Vorschau** zeigt „+ NAME = neu“ oder „~ NAME: alt -> neu“ (eine URL mit Zugangsdaten als „(set)“), prüft **alle** Rechner und Gruppen mit der Änderung („Würde brechen: …“, dann ist **Einstellung ändern** gesperrt) und sagt, wie viele Rechner die Datei betrifft. Hat ein Rechner der Datei einen **Server** (Marken auf diesem Computer), ändert die Seite `SERVER_PREFIX`, `PROVIDER`, `SERVER_LOCATION`, `GROUP_MACHINES`, `DNS_PROVIDER` und `DNS_DOMAIN` nicht (das gilt auch für Rechner, die nur eine Gruppe nennt, und für einen, der in zwei Gruppen steht): Zuerst `viper down`, denn `viper down` sucht dort, wo die Einstellungen es sagen, und sonst bliebe der bezahlte Server stehen. **Gefährliche** Werte (`K8S_DOCKER`/`COMPOSE_DOCKER` mit `dind` oder `remote`, `NATS_MCP_AUTOALLOW=all`, `SSH_ALLOWED_CIDRS`, `WORKSPACE_DIR`, und jedes Entfernen davon) verlangen, den Namen des Rechners, der Gruppe oder `.env.local` zu tippen. Geschrieben wird wie bei den Zugangsdaten: nur diese Anweisung, Kommentare und Rechte bleiben, eine seit der Vorschau geänderte Datei wird nicht ersetzt, und solange ein Lauf geht, ändert die Seite nichts. Eine Einstellung, die auch die Umgebung kennt, gewinnt in der Datei; beim Entfernen sagt die Vorschau, dass `viper` den Wert dann aus der Umgebung nimmt. **Frühere Fassungen:** Unter der Seite stehen die Sicherungen (Zeit, Dateien) mit **Herunterladen** (das ZIP, ohne Zugangsdaten) und **Wiederherstellen**. Wiederherstellen zeigt zuerst, was sich gegenüber den heutigen Dateien ändert, und prüft wie jede Änderung alle Rechner und Gruppen; **Diese Fassung wiederherstellen** schreibt sie dann zurück. Die **Zugangsdaten der heutigen Dateien bleiben stehen** (eine Sicherung enthält keine; ein Wert für ein Zugangsdatum im ZIP lässt die Seite ablehnen), ebenso URLs mit Zugangsdaten; Zugangsdaten, die irgendwo mit Wert genannt sind (in einem Kommentar, hinter einem Wert, im Text eines Befehls) oder in einer URL stehen (`https://benutzer:token@…`), sind in der Sicherung weggelassen; `ssh://git@host/…` bleibt. Darum geht ein Token, das **nur** in einem Kommentar oder in einem Befehl der heutigen Datei steht, beim Wiederherstellen verloren; die Vorschau sagt es. Ist die heutige Datei nicht lesbar, stellt die Seite nichts wieder her (sie kennte ihre Zugangsdaten nicht): Reparieren Sie sie am Terminal. Eine Datei, die schon wie in der Sicherung ist, wird übergangen; die anderen Dateien der Sicherung werden wiederhergestellt. Ein Name, der keine Einstellung dieser Seite ist (etwa `BASH_ENV`), verlangt wie ein geänderter Befehl das Wort `restore`: Die Dateien liest die Shell, und ein Name kann mehr tun, als eine Einstellung zu benennen. Eine Datei, die es nicht mehr gibt, wird nicht neu angelegt, und die Seite liest aus dem ZIP nur Dateien mit dem Namen einer Einstellungsdatei, als einfachen Text in der Größe einer Einstellungsdatei (Pfade, Rechte und Verknüpfungen nimmt sie nie aus dem Archiv). `SERVER_PREFIX`, `PROVIDER`, `SERVER_LOCATION`, `GROUP_MACHINES`, `DNS_PROVIDER` und `DNS_DOMAIN` ändert auch das Wiederherstellen nicht, solange ein Rechner des Dateikreises einen Server hat; ein geänderter Befehl `VIPER_POST_CLONE` oder gefährliche Werte verlangen das Wort `restore`. Auch das Wiederherstellen legt vorher eine Sicherung an. Jede Änderung, auch von Zugangsdaten, legt vorher eine an (`~/.local/state/viper/backups/`, höchstens fünf je Datei, nicht älter als 30 Tage).
13. **Zugangsdaten** (nur mit `--edit`): Die Tabelle nennt je Zugangsdatum (Token und Passwörter der Anbieter, GitHub, Claude, Code-Server), in welchen Dateien es gesetzt ist; Werte zeigt sie nie. Darunter wählen Sie das Zugangsdatum und die Datei (vorbelegt: die Datei, die es schon setzt, sonst `.env.local`), tippen den neuen Wert (mindestens 8 Zeichen, eine Zeile) und drücken **Vorschau**: sie sagt „+ gesetzt“ oder „~ ersetzt“, ohne Wert, und prüft wie beim Anlegen alle Rechner und Gruppen. **Wert setzen** schreibt nur diese eine Anweisung in der Datei; Kommentare, Reihenfolge und alles andere bleiben, die Rechte der Datei auch. Das Feld ist danach leer, der Wert steht nirgends auf der Seite, im Protokoll (`events.log` nennt nur den Namen) oder in einer Antwort, und die Ausgaben ersetzen ihn sofort durch `[hidden]`. **Aus der Datei entfernen** fragt in der Taste selbst noch einmal (mit Namen und Datei) und nimmt die Zeile heraus; hat die Umgebung von `viper ui` das Zugangsdatum, sagt die Vorschau, dass `viper` den Wert dann von dort nimmt. Hat sich die Datei seit der Vorschau geändert (zum Beispiel am Terminal), schreibt die Seite nichts und verlangt eine neue Vorschau. Während ein Lauf (Vorprüfung, Up, Down) geht, ändert sie keine Zugangsdaten. Abgelehnt werden auch ein Name, der kein Zugangsdatum ist, eine Datei, die ein Link ist, eine Datei mit Windows-Zeilenenden oder mit einem Wert, den `viper` nicht liest, und ein Zugangsdatum, das in der Datei mehrfach steht (am Terminal ändern). **Sicherung:** Vor jeder Änderung legt die Seite im Zustandsverzeichnis `~/.local/state/viper/backups/` ein ZIP an (`ZEIT-credential.zip`, `ZEIT-setting.zip` oder `ZEIT-restore.zip`, je nach Änderung; Rechte `0600`, einmal geschrieben und nie geändert): die Datei unter ihrem Namen, **alle Zugangsdaten darin leer** (`NAME=''`), und `manifest.yaml` (Zeit, Datei, Namen der geänderten Zugangsdaten, Prüfsummen des gesicherten Textes). Je Datei bleiben höchstens fünf Sicherungen, keine älter als 30 Tage. Ein ersetztes Token steht darum **nicht** in der Sicherung: Wer es zurückbraucht, trägt es neu ein.
14. **Protokolle:** die Ereignisse aller Rechner, eines Rechners oder dessen vollständige Ausgabe, mit Suchfeld; dasselbe wie `viper log`.

**Sprache:** Oben rechts wählen Sie Deutsch oder Englisch; ohne Wahl entscheidet die Sprache des Browsers, die Wahl bleibt im Browser erhalten. Die Beschriftungen in diesem Abschnitt sind die deutschen. In der englischen Ansicht heißen sie:

| Deutsch | Englisch |
|---|---|
| Übersicht, Läufe, NATS, Finden, Image, Befehl, Einstellungen, Ändern, Zugangsdaten, Anlegen, Protokolle | Overview, Runs, NATS, Find, Image, Command, Settings, Change, Credentials, Create, Logs |
| Vorschau, Datei anlegen, Bearbeiten an, Duplizieren | Preview, Create the file, edit on, Duplicate |
| Zustand: gestartet, angelegt, nicht gestartet, teilweise | State: started, created, not started, partly |
| Ohne Gruppe | No group |
| Filter, Alle Zustände, Filter löschen | Filter, All states, Clear the filter |
| Prüfen, Vorprüfung, Up, Down, Protokoll | Check, Preflight, Up, Down, Log |
| Gruppe vorprüfen, Gruppe: Up, Gruppe: Down | Preflight group, Up group, Down group |
| Abbrechen, Antwort, Senden | Cancel, Answer, Send |
| Peers, Themen, Mitlesen, Senden, Datei holen | Peers, Topics, Listen, Send, Get a file |
| Als Betreiber anmelden, Betreiber abmelden | Register as operator, Remove the operator |
| Datei entfernen | Remove the file |
| Sprechen als, Diese Datei holen, Datei herunterladen | Speak as, Fetch this file, Download the file |

Die Ausgaben von `viper` selbst (Läufe, Prüfungen, Protokolle) und die Fehler des Servers bleiben in beiden Ansichten englisch.

**Bedienung:** Die Seiten sind mit der Tastatur bedienbar (Sprunglink „Zum Inhalt springen“; die Seitenreiter wechseln Sie mit den Pfeiltasten, `Pos1` und `Ende`), haben Namen für Screenreader (zum Beispiel „Up für web“) und folgen dem hellen oder dunklen Farbschema des Systems. Endet die Sitzung (`viper ui` wurde neu gestartet), steht oben ein Hinweis; öffnen Sie dann die neue Adresse.

#### Optionen

* `--dir VERZEICHNIS`: das Verzeichnis mit den Einstellungen (Standard: das aktuelle).
* `--port N`: auf diesem Port lauschen (Standard: ein freier).
* `--no-open`: den Browser nicht öffnen; das Terminal zeigt die Adresse mit dem Schlüssel (gilt zehn Minuten, einmal).
* `--readonly`: keine Aktion anbieten (kein Preflight, Up, Down und kein NATS, auch kein Hochladen); Ansehen, Prüfen, Protokolle und Einstellungen bleiben.
* `--edit`: die Seiten **Anlegen** (neue Dateien mit Einstellungen, ohne Zugangsdaten), **Ändern** (eine Einstellung einer vorhandenen Datei, mit Sicherung) und **Zugangsdaten** (Token und Passwörter in vorhandenen Dateien setzen oder entfernen) anbieten. Die Kopfzeile zeigt „Bearbeiten an“. Zusammen mit `--readonly` bricht `viper ui` beim Start mit einer Meldung ab. Ohne `--edit` schreibt die Oberfläche keine Datei, und der Server lehnt Anfragen dazu ab.

#### Ergebnis

Die Seiten laufen im Browser. **Prüfen** und die Aktionen schreiben wie am Terminal in `events.log` und `logs/`; **Up**, **Down** und die NATS-Aktionen ändern, was auch am Terminal geändert würde.

#### Hinweise

* **Nur dieser Rechner:** Der Server lauscht auf `127.0.0.1` und antwortet nur unter genau dieser Adresse (nicht unter `localhost`). Die erste Anfrage tauscht einen Schlüssel, der bei jedem Start neu ist, einmal und höchstens eine Minute lang gegen ein Cookie (zehn Minuten mit `--no-open`); danach antwortet der Server nur mit diesem Cookie, das den Port im Namen trägt (zwei Oberflächen auf einem Rechner stören sich nicht). Der Browser wird über eine Datei in einem privaten Verzeichnis geöffnet, damit der Schlüssel nicht in der Befehlszeile eines Programms steht, das andere Benutzer sehen; `viper` löscht die Datei, sobald der Schlüssel benutzt ist, spätestens beim Beenden. Nach einem harten Abbruch bleibt sie im Temp-Verzeichnis liegen und enthält dann nur einen längst abgelaufenen Schlüssel.
* **Keine Zugangsdaten auf dem Bildschirm:** Die Seiten zeigen nie die Einstellungsdateien. Die Werte der bekannten geheimen Einstellungen (Token und Passwörter der Anbieter, GitHub, Claude, Code-Server) ersetzt `viper` in jeder Ausgabe durch `[hidden]`, auch in der ersten Zeile eines Laufs und in einem Geheimnis, das `viper` in zwei Teilen mit einer Pause dazwischen ausgibt; dafür erscheint ein Wort von acht und mehr Zeichen am Ende einer unfertigen Zeile erst nach einer zweiten Pause (eine Rückfrage wie „Type DELETE to continue:“ erscheint sofort). Steuerzeichen und Escape-Folgen fallen weg.
* **Grenzen:** Je Rechner läuft eine Aktion zugleich (auch Gruppenläufe halten ihre Mitglieder), höchstens vier Läufe der Aktionen und vier NATS-Läufe (zum Beispiel Mitleser) zugleich. Ein Tab hält einen Strom offen, insgesamt sind acht offene Ströme möglich; das Abbrechen geht auch dann noch. `Up` startet VS Code nicht (`--no-code`); eine Shell (`viper ssh`) gibt es in der Oberfläche nicht.
* **Aktualisierung:** Solange die Seite sichtbar ist, fragt sie alle fünf Sekunden (und beim Zurückkehren ins Fenster) nach einem kurzen Stand der Einstellungsdateien und der Marken dieses Computers; ändert er sich (zum Beispiel weil Sie am Terminal `viper up` gemacht oder eine Datei bearbeitet haben), lädt sie die Übersicht und die geöffnete Seite neu. Eine Vorschau, die auf „Anlegen“, „Ändern“ oder „Setzen“ wartet, wird dabei ungültig (die Zeile sagt es): Drücken Sie noch einmal **Vorschau**. Die Übersicht baut sich dabei neu auf, auch wenn ein Knopf der Tabelle den Fokus hat; der Fokus steht danach wieder auf demselben Knopf. Solange etwas läuft, fragt die Seite alle zwei Sekunden statt alle fünf. Die Seite baut nichts um, solange Sie in einem Formular der Seiten „Anlegen“, „Ändern“ oder „Zugangsdaten“ arbeiten (ein Feld hat den Fokus, oder ein zweiter Klick wird erwartet); sie holt es nach, sobald Sie weitergehen. Die Ausgabe laufender Läufe kommt unabhängig davon sofort. Eine Änderung der Einstellungen wartet höchstens 30 Sekunden auf die Änderung eines anderen `viper ui` und meldet es dann.
* **Beenden:** `Strg-C` bricht laufende Läufe sofort ab wie ein Strg-C am Terminal und wartet bis zu 20 Sekunden auf sie, auch wenn gerade eine lange Anfrage (ein Check) läuft.
* Hat ein Rechner unlesbare Einstellungen, steht der Fehler in der Zeile und **Prüfen** ist aus. Ein Rechner, der in mehreren Gruppen steht, erscheint je Gruppe einmal; **Prüfen** prüft ihn in der Gruppe der Zeile.
* Ist der Port belegt, bricht `viper ui` mit einer Meldung ab; wählen Sie mit `--port` einen anderen. Als `root` warnt `viper ui`: Starten Sie es als Ihr eigener Benutzer.
* Eine Seite, die der Server verweigert (kein Cookie, fremder Name) oder nicht kennt, zeigt eine kurze Erklärung auf Englisch und Deutsch.

### Mit Claude Code arbeiten

#### Vorgehen

1. Öffnen Sie in VS Code das integrierte Terminal, oder verbinden Sie sich mit `viper ssh <rechner>`.
2. Wechseln Sie in das Repository-Verzeichnis.
3. Starten Sie eine dauerhafte Sitzung und Claude Code:
   ```bash
   tmux new-session -A -s claude
   claude
   ```
4. Zum Verlassen der Sitzung drücken Sie **Strg+B**, dann **D**. Die Sitzung läuft weiter.
5. Verbinden Sie sich später erneut, am einfachsten mit `viper ssh <rechner> --tmux`, oder mit `viper ssh <rechner> --cmd "tmux attach -t claude"` (bei einem Befehl im Terminal fordert `viper ssh` ein Terminal an, das tmux braucht) oder von einem beliebigen SSH-Client mit `tmux attach -t claude`.

#### Ergebnis

Claude Code läuft in einer Sitzung, die einen Verbindungsabbruch übersteht.

#### Hinweise

* Docker (Engine, `docker buildx` und `docker compose`) kommt aus Dockers eigenem apt-Repository, nicht aus Ubuntus älterem Paket `docker.io`. Der Signaturschlüssel wird beim Einrichten gegen Dockers veröffentlichten Fingerabdruck geprüft. Beim Einrichten holt `package_upgrade` den neuesten Stand; die automatischen Ubuntu-Sicherheitsupdates erfassen Pakete aus Dockers Repository nicht, später aktualisieren Sie mit `sudo apt update && sudo apt upgrade`. Bei einem bestehenden Server mit `docker.io` wechseln Sie, indem Sie ihn neu anlegen.
* Neue Server bekommen eine `~/.tmux.conf` für `builder` (Maus an, Fenster ab 1 nummeriert, `Esc` ohne Verzögerung, großer Verlauf). Mit **Alt+1** bis **Alt+9** wechseln Sie direkt zwischen Fenstern; sonst gelten die üblichen Kürzel (**Strg+B**, dann **N**/**P** oder eine Ziffer, **C** für ein neues Fenster). Mit eingeschalteter Maus markieren Sie Text zum Kopieren mit gedrückter **Umschalttaste**. Auf einem bestehenden Server wirkt die Datei nicht; legen Sie sie dort von Hand an. Eigene Änderungen gehen mit dem Server verloren.
* Neue Server stellen für VS Code Remote-SSH das Terminal-Profil `tmux` als Standard ein (`~/.vscode-server/data/Machine/settings.json`). Jedes neue VS-Code-Terminal hängt sich an die tmux-Sitzung `claude` an oder legt sie an. Schließen Sie VS Code, bleibt die Shell samt laufender Programme erhalten, und nach dem erneuten Verbinden zeigt ein neues Terminal sie wieder. Mehrere Terminals zeigen dieselbe Sitzung und spiegeln dasselbe tmux-Fenster: Eingaben und Ausgaben erscheinen in allen. Für getrennte Shells legen Sie darin tmux-Fenster an (**Strg+B**, dann **C**). VS-Code-Tasks und Erweiterungen starten dagegen eine normale `bash`, nicht die tmux-Sitzung. Auf einem bestehenden Server wirkt die Einstellung nicht; tragen Sie sie dort von Hand ein.
* Homebrew (`brew`) wird beim ersten Einrichten mit dem offiziellen Installationsskript nach `/home/linuxbrew/.linuxbrew` installiert und ist in allen Shells verfügbar, auch für Claude Code. Das kostet ein bis zwei Minuten. Mit `VIPER_HOMEBREW=0` (etwa in `machines/<rechner>.env.local`) entfällt es. Scheitert die Installation, warnt `viper up` nur; ein erneuter `viper up` versucht es wieder.
* Interaktive Shells starten im Arbeitsverzeichnis des Rechners (beim geklonten Repository), wenn sie im Home-Verzeichnis beginnen würden. Das gilt für `viper ssh`, tmux und einen normalen SSH-Login. VS-Code-Terminals öffnen ohnehin im Projektordner, und einzelne SSH-Befehle sind nicht betroffen.
* Ohne `VIPER_CLAUDE_TOKEN` müssen Sie das Anmelden bei Claude nach jedem neuen Server wiederholen, weil `/home/builder/.claude` mit dem Server gelöscht wird. Mit Token entfällt das (siehe „Claude Code ohne erneute Anmeldung nutzen“).

### Mehrere Rechner gleichzeitig betreiben

#### Vorgehen

Starten Sie für jedes Repository einen Rechner mit eigenem Namen:

```bash
viper up web --repo https://git.example.org/team/web.git
viper up api --repo https://git.example.org/team/api.git
```

Jeder Rechner hat einen eigenen Server, eine eigene Platte und einen eigenen DNS-Namen. Sie beenden sie getrennt mit `viper down web` und `viper down api`. Gehören die Rechner zusammen, starten und beenden Sie sie mit einer Gruppendatei auf einmal (siehe „Eine Gruppe von Rechnern gemeinsam starten und beenden“).

### Einen Rechner als Pod in einem Kubernetes-Cluster betreiben (`PROVIDER=kubernetes`)

Mit `PROVIDER=kubernetes` ist ein „Rechner“ ein **Pod** in einem Kubernetes-Cluster und keine VM. Sie arbeiten mit denselben Befehlen wie sonst (`up`, `check`, `ssh`, `vscode`, `down`, Gruppen, NATS), brauchen aber kein Cloud-Konto, nur einen Cluster, den Ihre `kubeconfig` kennt.

#### Voraussetzung

* Ein Kubernetes-Cluster und sein **Kontext** in Ihrer `kubeconfig`, bei k3d zum Beispiel `k3d cluster create viper` mit dem Kontext `k3d-viper`. Ihr Zugang muss im Namespace StatefulSets, Secrets und Volumes anlegen und löschen sowie Ports von Pods weiterleiten dürfen; `viper preflight` fragt das beim Cluster ab.
* Docker auf Ihrem Rechner, um das Image der Rechner zu bauen, und bei k3d das Programm `k3d`.
* Docker auf dem Rechner (Standard) braucht einen Namespace, der privilegierte Pods erlaubt. Legt `viper` den Namespace selbst an, ist das passend eingestellt.
* Die Pods brauchen Internetzugang (Homebrew-Pakete, Claude-Installer, Git).

#### Vorgehen

1. **Image bauen.** Diesen Schritt können Sie überspringen: Ohne `K8S_IMAGE` nimmt `viper` das veröffentlichte Image seiner Version (`ghcr.io/pt9912/viper-machine:<Version>`). Sonst baut `viper image build` das Image mit Docker aus den Dateien, die in `viper` stecken; Sie brauchen weder das Repository noch `make`. Das dauert beim ersten Mal einige Minuten, und das Image ist mehrere GB groß. Statt zu bauen können Sie das veröffentlichte, öffentliche Image `ghcr.io/pt9912/viper-machine:<Version>` nehmen (die Version, die `viper version` meldet; amd64 und arm64): Tragen Sie seinen Namen in Schritt 2 als `K8S_IMAGE` ein; ein Cluster mit Internetzugang (auch k3d) lädt es beim ersten Start selbst, alternativ spielen Sie es bei k3d mit `docker pull` und `k3d image import <name> -c viper` ein. Bei **k3d** kommt das gebaute Image mit `viper image build --k3d viper` gleich in den Cluster `viper`. Bei jedem anderen Cluster laden Sie es mit `docker push` in ein Register, das der Cluster erreicht. Verlangt das Register eine Anmeldung, legen Sie im Namespace ein Secret an (`kubectl -n viper create secret docker-registry regcred --docker-server=… --docker-username=… --docker-password=…`) und tragen seinen Namen in Schritt 2 als `K8S_IMAGE_PULL_SECRET` ein. Die Ausgabe nennt den Namen des Images; ihn tragen Sie in Schritt 2 als `K8S_IMAGE` ein (`--tag NAME:TAG` wählt einen eigenen).
2. **Einstellungen** in `machines/<rechner>.env.local` oder in der Gruppendatei (nicht in der allgemeinen `.env.local`):

   ```
   PROVIDER=kubernetes
   DNS_PROVIDER=none
   SERVER_LOCATION=k3d-viper
   # K8S_IMAGE=<Name aus Schritt 1>   (nur für ein selbst gebautes oder anderes Image)
   SERVER_TYPE=2x4
   ```

   `SERVER_LOCATION` ist der Kontext der `kubeconfig` und Pflicht; `K8S_IMAGE` ist nur für ein selbst gebautes oder anderes Image nötig; `SERVER_TYPE` ist `<CPUs>x<GiB Speicher>`. Weitere Einstellungen und Standardwerte: Kapitel 4, „Kubernetes: Einstellungen“.
3. `viper preflight <rechner>`, dann `viper up <rechner> [--repo URL]`. Danach arbeiten Sie wie bei jedem Rechner: `viper ssh <rechner>`, `viper vscode <rechner>`, `viper check <rechner>`. Zum Löschen: `viper down <rechner>`.

#### Ergebnis

`viper up` meldet „The machine is the pod …“ und am Ende „Ready“ mit dem Arbeitsverzeichnis. Im Namespace `viper` läuft der Pod `<präfix>-<rechner>-0`; `viper check` zeigt für ihn „a pod: no cloud-init“, den Docker-Zustand und die Härtung von `sshd`. Das Repository liegt in `/home/builder/work/<repo>`.

#### Hinweise und Warnungen

* **Zugang:** Der Pod hat weder Adresse noch DNS-Namen; `viper` verbindet sich über die API des Clusters. `ssh`, `scp`, `viper ssh` und SSH-Tunnel funktionieren wie sonst.
* **Daten:** Das Heimverzeichnis mit Arbeitsverzeichnis und Repository und das Verzeichnis von Homebrew liegen auf je einem Volume. Sie überstehen das Ersetzen des Pods, auch was Sie mit `brew install` nachinstalliert haben. `viper down` löscht beide mit allen Daten, nach der Abfrage `DELETE`. `df -h` ohne Pfad zeigt die Volumes nicht, wenn sie auf demselben Gerät liegen wie das Wurzelverzeichnis (bei k3d und Docker der Fall): `df -h /home/builder /home/linuxbrew` zeigt sie, `viper check` nennt den freien Platz des Heimverzeichnisses.
* **Docker (`K8S_DOCKER`):** Standard ist `dind`, ein Docker-Daemon als zweiter, **privilegierter** Container im Pod. Wer ihn übernimmt, hat Root auf dem Knoten: Nutzen Sie das in k3d oder einem eigenen Cluster, nicht in einem geteilten. Wo privilegierte Pods verboten sind, nehmen Sie `remote` (ein Daemon außerhalb, `K8S_DOCKER_HOST`) oder `none` (kein Docker). **Bind-Mounts:** Ein Pfad bei `docker run -v` meint einen Pfad des Daemons. Bei `dind` ist das Heimverzeichnis des Rechners (`/home/builder`, mit dem Arbeitsverzeichnis) auch im Docker-Container unter demselben Pfad eingehängt, sodass `docker run -v /home/builder/work/<repo>:/repo …` die Dateien sieht; andere Pfade (`/tmp`) sieht der Daemon nicht, und bei einem Rechner, den es schon gibt, gilt es erst nach `viper down` und neuem `viper up`. Bei `remote` gelten die Pfade auf dem fernen Host.
* **NATS-Gruppen** funktionieren wie sonst. Der Server der Gruppe ist im Cluster unter `viper-nats-<gruppe>.<namespace>.svc` erreichbar. Ersetzt der Cluster seinen Pod, arbeiten die Clients ohne neue Anmeldung weiter. **Der Schutz des Ports im Netz setzt voraus, dass der Cluster NetworkPolicies durchsetzt** (k3d tut es; bei anderen Clustern hängt es vom Netz-Plugin ab).
* Startet ein Pod nicht oder bleiben Reste im Cluster, hilft Kapitel 7.

### Einen Rechner als Container auf dem eigenen Docker-Host betreiben (`PROVIDER=compose`)

Mit `PROVIDER=compose` ist ein „Rechner“ ein **Container** auf dem Docker-Host Ihres eigenen Computers. `viper up` schreibt dafür eine `compose.yaml` und startet sie mit `docker compose`. Sie arbeiten mit denselben Befehlen wie sonst (`up`, `check`, `ssh`, `vscode`, `down`, Gruppen) und brauchen weder ein Cloud-Konto noch einen Cluster, nur Docker. NATS-Gruppen gehen auch (siehe unten).

#### Voraussetzung

* Docker mit dem Plugin `docker compose` auf Ihrem Computer; der Daemon läuft auf **diesem** Computer (ein Docker-Host über `ssh://` wird nicht unterstützt, weil die Schlüssel des Rechners als Dateien in den Container eingehängt werden). Geprüft ist Linux; Docker Desktop (Windows, macOS) ist nicht einzeln geprüft.
* Das Recht, den Docker-Socket zu benutzen.
* Der Container braucht Internetzugang (Homebrew-Pakete, Claude-Installer, Git).

#### Vorgehen

1. **Image beschaffen:** Der Schritt entfällt: Ohne `COMPOSE_IMAGE` nimmt `viper` das veröffentlichte Image seiner Version (`ghcr.io/pt9912/viper-machine:<Version>`) und lädt es bei Bedarf selbst. Sonst bauen: `viper image build`. Das Image liegt danach im Docker-Host und braucht kein Register; die Ausgabe nennt den Namen (`--tag NAME:TAG` wählt einen eigenen). Oder das veröffentlichte Image nehmen: `ghcr.io/pt9912/viper-machine:<Version>` mit der Version, die `viper version` meldet (öffentlich, mehrere GB, amd64 und arm64). Der Name kommt in Schritt 2 als `COMPOSE_IMAGE` hinein; fehlt das Image auf dem Docker-Host, lädt `viper up` es selbst mit `docker pull` (ein Name ohne Registry, wie ihn `viper image build` vergibt, wird nicht geladen). Das Laden dauert bei mehreren GB einige Minuten; währenddessen meldet `viper up` alle 20 Sekunden „still pulling the image … (1m20s)“. Bei einer Gruppe lädt **ein** Rechner das Image, die anderen melden nach 20 Sekunden „waiting for the pull of the image …“ (und „The image is there now.“, wenn es da ist) und gehen danach gleich weiter (die Ausgabe zeigt kein Fortschrittsbalken von `docker`).
2. **Einstellungen** in `machines/<rechner>.env.local` oder in der Gruppendatei (nicht in der allgemeinen `.env.local`):

   ```
   PROVIDER=compose
   DNS_PROVIDER=none
   SERVER_LOCATION=default
   # COMPOSE_IMAGE=<Name aus Schritt 1>   (nur für ein selbst gebautes oder anderes Image)
   COMPOSE_DOCKER=dind
   SERVER_TYPE=2x4
   ```

   `SERVER_LOCATION` ist der Docker-Kontext (`default` ist der lokale Daemon) und Pflicht, ohne Standard. Meldet der Preflight „context not found“, ist `SERVER_LOCATION` kein Docker-Kontext, oft steht dort noch ein Hetzner-Standort aus der `.env.local`: Setzen Sie ihn für den Rechner oder die Gruppe, z. B. auf `default`. `COMPOSE_IMAGE` ist nur für ein selbst gebautes oder anderes Image nötig. `SERVER_TYPE` ist `<CPUs>x<GiB Speicher>`. `COMPOSE_DOCKER=dind` müssen Sie ausdrücklich eintragen: Der Docker im Rechner ist ein **privilegierter** Container, und der Preflight meldet sonst einen Fehler (siehe Hinweise).
3. `viper preflight <rechner>`, dann `viper up <rechner> [--repo URL]`. Danach `viper ssh <rechner>`, `viper vscode <rechner>`, `viper check <rechner>`. Zum Löschen: `viper down <rechner>`.

#### Ergebnis

`viper up` meldet „The machine is the container …“ und am Ende „Ready“ mit dem Arbeitsverzeichnis. Auf dem Docker-Host laufen die Container `<präfix>-<rechner>` und mit `dind` zusätzlich `<präfix>-<rechner>-docker`. Das Repository liegt in `/home/builder/work/<repo>`. `docker run` im Rechner startet Container im Docker des Rechners, nicht auf Ihrem Host.

#### Hinweise und Warnungen

* **Zugang:** Der Container hat keine Adresse und keinen veröffentlichten Port; `viper` verbindet sich mit `docker exec` (`viper docker-proxy`). `ssh`, `scp` und `viper ssh` funktionieren wie sonst.
* **Daten:** Heimverzeichnis (mit Arbeitsverzeichnis) und Homebrew liegen auf zwei Docker-Volumes `<name>-home` und `<name>-brew`. Sie überstehen einen Neustart des Containers und `viper up` auf einer vorhandenen Maschine; `viper up` legt einen vorhandenen Container nie neu an (eine geänderte Größe oder ein anderes Image gilt erst nach `viper down` und neuem `viper up`). `viper down` löscht beide mit allen Daten, nach den Abfragen.
* **Docker im Rechner (`COMPOSE_DOCKER`):** `dind` ist ein Docker-Daemon in einem **privilegierten** zweiten Container. Wer ihn übernimmt, hat Root auf Ihrem Host: Nutzen Sie ihn nur auf Ihrem eigenen Computer. Statt dessen gehen `remote` (ein Daemon außerhalb, `COMPOSE_DOCKER_HOST`) und `none`. **Bind-Mounts:** Ein Pfad bei `docker run -v` meint einen Pfad des Docker-Daemons, nicht des Rechners. Bei `dind` ist das Heimverzeichnis des Rechners (`/home/builder`, mit dem Arbeitsverzeichnis) im Docker-Dienst unter demselben Pfad eingehängt: `docker run -v /home/builder/work/<repo>:/repo …` sieht die Dateien des Rechners. Andere Pfade des Rechners (zum Beispiel `/tmp`) sieht der Daemon nicht; bei einem Rechner, den es schon gibt, gilt das Einhängen erst nach `viper down` und neuem `viper up`. Bei `remote` gelten die Pfade auf dem fernen Host, dort sieht der Daemon die Dateien des Rechners nicht (kopieren Sie sie hinein, oder nutzen Sie `docker cp`). Werkzeuge, die ein Verzeichnis einhängen (ein Gate mit `make` im Container), melden sonst ein leeres Verzeichnis. Die Images im Docker des Rechners gehen verloren, wenn der Container neu angelegt wird. Wird nur der Rechner-Container neu gestartet (Absturz, `docker restart`), startet `viper up` den zweiten Container neu und wartet, bis der Daemon antwortet.
* **`DOCKER_HOST`:** Ist die Umgebungsvariable gesetzt, nimmt Docker sie statt des Kontexts aus `SERVER_LOCATION`. `viper` verweigert dann die Arbeit mit einer Meldung, damit nichts auf dem falschen Host entsteht: Heben Sie die Variable für `viper` auf (`unset DOCKER_HOST`). Rechnernamen dürfen nicht auf `-docker` enden (so heißt der Container des Docker-Dienstes).
* **Schlüssel:** Die Schlüssel des Rechners liegen als Dateien im Zustandsverzeichnis (`~/.local/state/viper/compose/g_<gruppe>/secret/<rechner>/` bei einem Rechner in einer Gruppe, `~/.local/state/viper/compose/<name>/secret/` sonst; nur für Sie lesbar) und bleiben bis `viper down`. Neue erlaubte Schlüssel gelten erst für einen neu angelegten Rechner.
* Startet ein Container nicht oder bleiben Reste auf dem Docker-Host, hilft Kapitel 7.

### Rechner über NATS verbinden (NATS-Gruppe)

NATS ist ein schlankes Nachrichtensystem. Mit einer NATS-Gruppe verbinden Sie mehrere Rechner so, dass Programme auf ihnen einander Nachrichten schicken können (zum Beispiel Status melden oder Aufträge verteilen). Die Funktion ist optional und standardmäßig aus. Es läuft kein Verwaltungsdienst und es braucht keine zusätzliche Datenbank.

#### Voraussetzung

* Der Anbieter ist Hetzner Cloud (`PROVIDER=hetzner`), AWS (`PROVIDER=aws`), Google Cloud (`PROVIDER=gcp`) oder Azure (`PROVIDER=azure`), ein Kubernetes-Cluster (`PROVIDER=kubernetes`) oder ein Docker-Host (`PROVIDER=compose`). **Hetzner, k3d und Docker Compose sind in echten Läufen getestet. AWS, Google Cloud und Azure sind gebaut und gegen nachgebildete Schnittstellen getestet; ein Lauf in einem echten Konto ist dort noch nicht belegt.** `viper preflight` prüft bei AWS (Probelauf `DryRun`) und Google Cloud (`testIamPermissions`), ob das Konto die Rechte für die Gruppe hat, und `viper check` meldet, wenn die Regel der Gruppe bei AWS oder Google Cloud von dem abweicht, was `viper` anlegt (zum Beispiel ein anderer Port oder eine Quelle außerhalb der Gruppe).
* **Docker Compose:** Alle Rechner der Gruppe liegen auf demselben Docker-Host. `viper` legt das Docker-Netz `viper-nats-<gruppe>` selbst an (nicht Compose, damit es nur einmal entsteht und beim letzten Rechner wieder verschwindet) und hängt jeden Rechner der Gruppe **nur** an dieses Netz; ein Container, der nicht daran hängt, erreicht den Port nicht. Der Server ist im Netz unter dem Namen `viper-nats-<gruppe>` erreichbar (darauf lautet sein Zertifikat); ersetzt Docker seine Adresse, arbeiten die Clients ohne neue Anmeldung weiter. Rolle und Gruppe stehen als Labels am Container, der Zustand in kleinen Volumes `viper-nats-<gruppe>-<rechner>-<zustand>`. Ein neuer Rechner tritt der Gruppe beim Anlegen bei. Nur ein Rechner, der **ohne** NATS angelegt wurde und dem Sie später `NATS_ENABLED` geben, kann nicht nachträglich beitreten (Docker kann die Labels eines Containers nicht ändern): `viper up` meldet das, und `viper down` mit anschließendem `viper up` legt ihn mit der Gruppe neu an. Jedes Docker-Netz belegt ein /16 aus dem Adressbereich des Daemons (er reicht für etwa 30 Netze).
* **Azure:** Alle Rechner der Gruppe müssen in derselben Region liegen. `viper` legt in der Ressourcengruppe eine Anwendungssicherheitsgruppe `viper-nats-<gruppe>` an; die Netzwerkkarten der Rechner kommen hinein, und die Netzwerksicherheitsgruppe jedes Rechners bekommt die Regel `viper-nats-<gruppe>` (der NATS-Port von dieser Gruppe zu dieser Gruppe). Rolle und Zustand stehen als Tags `viper-nats-*` an der VM. **Die Sperre des ersten Starts ist nicht belegt:** Das Anlegen der Anwendungssicherheitsgruppe verlangt `If-None-Match: *`; ob Azure das bei dieser Ressource durchsetzt, ist nicht an einem echten Konto geprüft. Ignoriert Azure es, können zwei gleichzeitige erste Starts beide Server werden; `viper check` meldet dann „the group has 2 servers“. Starten Sie den ersten Rechner einer Gruppe deshalb zunächst allein. Wie bei Google Cloud macht die Regel nichts enger als `AllowVnetInBound`.
* **Google Cloud:** Alle Rechner der Gruppe müssen im selben Netzwerk (`GCP_NETWORK`) und in **derselben Zone** liegen (die Labels der Rechner werden je Zone gelesen). `viper` legt kein Netzwerk an, sondern nur eine Firewall-Regel `viper-nats-<gruppe>` (der NATS-Port von und zu Rechnern mit dem Netzwerk-Tag `viper-nats-<gruppe>`) und setzt dieses Tag an die Rechner der Gruppe. Rolle und Zustand stehen als Labels `viper-nats-*` an der VM. **Die Regel macht nichts enger:** Netzwerke, die allen Verkehr im Inneren erlauben (das Netzwerk `default` mit `default-allow-internal`), erreichen den Port auch ohne sie; geschützt ist er durch den NATS-Server selbst (nur auf der privaten Adresse, nur registrierte Schlüssel, das Zertifikat). Das Konto braucht zusätzlich `compute.firewalls.create/delete/get` und `compute.instances.setTags/setLabels`.
* **AWS:** Alle Rechner der Gruppe müssen im selben VPC liegen (`AWS_SUBNET_ID`, `AWS_VPC_ID`) und sich über private Adressen erreichen. `viper` legt kein Netzwerk an und ändert keines, sondern nur eine Security Group `viper-nats-<gruppe>`, die den NATS-Port nur von Mitgliedern dieser Gruppe zulässt (zusätzlich zu den Schutzschichten, die auch bei Hetzner gelten: Server nur auf der privaten Adresse, `ufw`, registrierte Schlüssel, Zertifikat). Die Rolle und der Zustand stehen als Tags `viper-nats-*` an der Instanz. Dazu braucht das Konto zusätzlich die Rechte `ec2:CreateSecurityGroup`, `AuthorizeSecurityGroupIngress`, `DeleteSecurityGroup`, `ModifyInstanceAttribute` und `CreateTags`. Verkehr zwischen Availability Zones kostet bei AWS etwas.
* Alle Rechner einer Gruppe liegen im selben Hetzner-Projekt und in derselben Netzwerkzone (zum Beispiel `fsn1`, `nbg1` und `hel1` gehören zu `eu-central`, `ash` zu `us-east`).
* Rechnernamen und Gruppenname bestehen aus 1 bis 31 Kleinbuchstaben, Ziffern und Bindestrichen und beginnen und enden mit Buchstabe oder Ziffer. Ein vorhandener Rechner mit anderem Namen (etwa mit Unterstrich) kann nicht aufgenommen werden.

#### Vorgehen

1. Tragen Sie in `.env.local` (oder in `machines/<rechner>.env.local`) ein:

   ```
   NATS_ENABLED=true
   NATS_GROUP=entwicklung
   ```

   `NATS_PORT` (Standard `4222`) brauchen Sie nur, wenn der Port abweichen soll.
2. Starten Sie die Rechner wie gewohnt:

   ```bash
   viper up alpha
   viper up beta
   viper up gamma
   ```

   Der **erste** Rechner einer Gruppe wird der NATS-Server, alle weiteren sind Clients. Sie dürfen die Rechner auch gleichzeitig starten: Die Clients warten dann, bis der Server fertig ist. Bequemer geht es mit einer Gruppendatei und `viper up --group` (siehe „Eine Gruppe von Rechnern gemeinsam starten und beenden“).
3. Prüfen Sie mit `viper check alpha` (und für die anderen Rechner genauso).

#### Ergebnis

Jeder Rechner der Gruppe hat im Verzeichnis `~/.viper-nats/` die Datei `connection.env` mit Adresse, Zertifikat und Schlüsseldatei:

```
NATS_URL=tls://10.115.0.2:4222
NATS_CA_FILE=/home/builder/.viper-nats/server.crt
NATS_NKEY_SEED_FILE=/home/builder/.viper-nats/client.nk
NATS_INBOX_PREFIX=_INBOX.beta   # wird seit v0.15.1 nicht mehr gebraucht
VIPER_NATS_SUBJECT_PREFIX=viper.entwicklung.beta
```

Auf allen Rechnern ist außerdem das Programm `nats` installiert. Beispiel auf `beta`:

```bash
. ~/.viper-nats/connection.env
nats --server $NATS_URL --tlsca $NATS_CA_FILE --nkey $NATS_NKEY_SEED_FILE pub viper.entwicklung.beta.status "bereit"
```

**Wer darf was?** Jeder Rechner darf nur in seinen eigenen Bereich `viper.<gruppe>.<rechner>.>` senden und Anfragen an `viper.<gruppe>.<empfänger>.req.>` schicken. Mitlesen darf er alles in `viper.<gruppe>.>`. Alles andere, auch andere Gruppen, wird abgelehnt. **Antworten sind normale Nachrichten:** Wer antwortet, veröffentlicht sie in seinem eigenen Bereich, zum Beispiel `viper.<gruppe>.<antwortender>.out.<anfragender>`; das Antwort-Verfahren von NATS über ein Reply-Subject (`nats request`) funktioniert zwischen den Rechnern seit v0.15.1 nicht mehr. In `viper.<gruppe>.<rechner>.>` steht verlässlich, wer gesendet hat; in `viper.<gruppe>.<empfänger>.req.>` nicht, diesen Bereich darf jede Maschine beschreiben. **Alle Rechner der Gruppe können alles mitlesen**, nichts ist privat. Gruppen aus v0.15.0 hatten eine Lücke (Rechner konnten über die Antwort-Funktion `allow_responses` auch in fremde Bereiche senden); ein weiteres `viper up` auf einem Rechner der Gruppe schreibt die Konfiguration neu und schließt sie.

#### Hinweise

* **Sicherheit:** Der Server lauscht nur im privaten Netzwerk (`10.x.x.x`) der Gruppe und ist aus dem Internet nicht erreichbar. Die Verbindung ist mit TLS verschlüsselt, und jeder Rechner meldet sich mit einem eigenen Schlüssel (NKey) an. Der geheime Schlüssel (`client.nk`) bleibt auf dem jeweiligen Rechner.
* **Lebensdauer:** Das Zertifikat des Servers gilt einen Monat und wird nicht erneuert. NATS-Gruppen sind für Zeiträume von einem Tag bis zu wenigen Wochen gedacht. `viper check` warnt, wenn weniger als sieben Tage bleiben. Läuft das Zertifikat ab, legen Sie die Gruppe neu an.
* **Der Server ist ein normaler Rechner.** Solange die Gruppe weitere Rechner hat, lässt `viper down` ihn nicht löschen. Löschen Sie zuerst die anderen Rechner. Mit `viper down <server> --force` beenden Sie die Gruppe trotzdem; dazu tippen Sie den Gruppennamen ein, und die anderen Rechner verlieren ihre Verbindung.
* **Beim Löschen eines Clients** (`viper down beta`) entfernt `viper` zuerst seinen Schlüssel beim Server. Erst danach wird der Rechner gelöscht. Ist der Server nicht erreichbar, bleibt der Rechner bestehen; mit `--force` löschen Sie ihn trotzdem, der Eintrag bleibt dann bis zum Ende der Gruppe in der Serverkonfiguration.
* Mit dem letzten Rechner der Gruppe löscht `viper down` auch das Netz der Gruppe `viper-nats-<gruppe>` (bei Kubernetes: die NetworkPolicy und den Service).
* Ein Rechner wechselt nicht stillschweigend die Gruppe: Ändern Sie `NATS_GROUP` für einen vorhandenen Rechner, bricht `viper up` ab. Löschen Sie ihn zuerst.
* Nicht enthalten: mehrere Server, automatischer Ersatz eines ausgefallenen Servers, dauerhaft gespeicherte Nachrichten (nur Dateien werden gespeichert, siehe „Bilder, PDFs und ZIP-Dateien senden“), Zugriff von außen und Gruppen über mehrere Anbieter.

### Eine Gruppe von Rechnern gemeinsam starten und beenden (`groups/`)

Statt jeden Rechner einzeln zu starten, legen Sie eine Gruppendatei an. Sie nennt die Rechner und die Einstellungen, die für alle gelten. `viper` startet, prüft und beendet sie dann gemeinsam und **gleichzeitig**: Drei Rechner brauchen so etwa so lange wie einer (im Test siebeneinhalb Minuten).

#### Voraussetzung

* `viper setup` ist gelaufen. Es legt die Vorlage `groups/example.env.example` an.
* Für eine NATS-Gruppe gelten die Voraussetzungen des Kapitels „Rechner über NATS verbinden“. Ohne NATS ist die Gruppendatei nur eine Liste von Rechnern mit gemeinsamen Einstellungen.

#### Vorgehen

1. Kopieren Sie die Vorlage nach `groups/<gruppe>.env`. Der Dateiname ist der Name der Gruppe (1 bis 31 Kleinbuchstaben, Ziffern, Bindestriche) und zugleich `NATS_GROUP`:

   ```
   GROUP_MACHINES=alpha,beta,gamma
   NATS_ENABLED=true
   NATS_MCP=true
   ```

   Weitere Zeilen sind Einstellungen für alle Rechner der Gruppe. Einen eigenen `SERVER_TYPE`, `SERVER_LOCATION` oder `SERVER_IMAGE` setzen Sie je Rechner in `machines/<rechner>.env.local`; eine solche Datei brauchen die Rechner sonst nicht.
2. Starten Sie die Gruppe. Zuerst laufen die Prüfungen für alle Rechner; scheitert eine, wird **nichts** angelegt:

   ```bash
   viper up --group entwicklung
   ```
3. Prüfen Sie die Gruppe und führen Sie bei Bedarf einen Befehl auf allen Rechnern aus:

   ```bash
   viper check --group entwicklung
   viper ssh --group entwicklung --cmd 'uptime'
   ```
4. Beenden Sie die Gruppe:

   ```bash
   viper down --group entwicklung
   ```

   `viper` fragt für jeden Rechner einzeln (`[beta] Type DELETE to continue:`), immer nur einen zugleich.

| Befehl | Wirkung |
|---|---|
| `viper up --group g [--repo URL]` | startet alle Rechner der Gruppe |
| `viper down --group g [--force]` | beendet alle Rechner der Gruppe |
| `viper check --group g` | prüft alle Rechner |
| `viper preflight --group g [--repo URL]` | prüft vorab alle Rechner, nur lesend; legt nichts an |
| `viper ssh --group g --cmd 'befehl'` | führt einen Befehl auf allen Rechnern aus (ohne `--cmd` nicht möglich) |

`--parallel N` begrenzt, wie viele Rechner gleichzeitig laufen (Standard 8). `--group` und ein Rechnername zusammen sind ein Fehler.

#### Ergebnis

Jede Ausgabezeile trägt den Namen des Rechners (`[alpha] …`), am Ende steht eine Tabelle mit dem Ergebnis je Rechner. Der Exit-Status ist 0, wenn alle in Ordnung sind, 1 bei einem Fehler und 130 bei einem Abbruch mit Strg-C. VS Code wird bei `up --group` nicht geöffnet (`viper vscode <rechner>` einzeln).

Bei `down --group` fragt `viper` die Rechner nacheinander (`DISCARD`, `DELETE`). Damit die Frage nicht zwischen den Zeilen anderer Rechner untergeht, zeigt `viper` die Ausgabe eines Rechners erst, wenn er fragt oder fertig ist, dann am Stück direkt über der Frage. Deshalb erscheinen die Zeilen eines Rechners bei `down --group` nicht laufend, sondern in Blöcken.

#### Hinweise

* **Rangfolge der Einstellungen:** `.env.local`, dann die Gruppe, dann `machines/<rechner>.env.local` (der Rechner gewinnt). Setzt die Datei eines Rechners eine andere `NATS_GROUP` als die Gruppe, bricht `viper` ab.
* **Ein Rechner darf in mehreren Gruppen stehen.** Dann gelten die Einstellungen **einer** Gruppe, und Sie sagen bei jedem Befehl für diesen Rechner, welcher: `viper up web --in-group dev`, ebenso `down`, `check`, `ssh`, `preflight`, `vscode`, `dns` und `log`. Ohne `--in-group` bricht `viper` mit einer Meldung ab, die die Gruppen nennt, **außer der Rechner läuft schon**: Er wird in der Gruppe verwaltet, in der er mit `viper up` gestartet wurde (`viper` merkt sie sich bis zum `viper down`), und `ssh`, `check`, `down` usw. nehmen diese Gruppe ohne Option. Eine andere Gruppe wird dann abgelehnt, mit dem Hinweis, wie es geht (`viper down <rechner> --in-group <gruppe>`, danach in der anderen Gruppe starten). Der Rechner ist ein Server: Zwei Gruppen mit demselben `SERVER_PREFIX` teilen sich ihn; wollen Sie ihn in beiden getrennt, geben Sie den Gruppen verschiedene Präfixe. `--group dev` bleibt der Befehl für **alle** Rechner der Gruppe (`viper up --group dev`); dort gelten für jeden Rechner die Einstellungen von `dev`, auch wenn er noch in einer anderen Gruppe steht. Bei einem Rechner in einer Gruppe ist die NATS-Gruppe der Name der gewählten Gruppendatei (`NATS_GROUP` in einer Rechnerdatei, das dem widerspricht, ist ein Fehler); eine andere Gruppe desselben Rechners kann ohne NATS sein. Ein Rechner ohne Gruppe setzt `NATS_GROUP` weiter in seiner eigenen Datei. Steht ein Rechner in mehreren Gruppen, merkt sich seine SSH-Konfiguration die Gruppe (beim nächsten `viper up`), und `ssh hetzner-…` und VS Code brauchen keine Option; steht er in einer Gruppe, ändert sich an der SSH-Konfiguration nichts. `viper log <rechner>` verlangt die Auswahl ebenfalls, weil der Name des Protokolls vom Präfix der Gruppe abhängt. Innerhalb einer Datei steht jeder Name nur einmal in `GROUP_MACHINES` (mit Leerzeichen den ganzen Wert in Anführungszeichen setzen). Ein Tippfehler im Namen legt einen **neuen** Server an; `viper up --group` nennt vorher die Rechner ohne eigene Datei, damit Sie das sehen.
* **Einzelne Rechner:** Auch `viper up alpha` für einen Rechner der Gruppe nutzt deren Einstellungen. Ist die Datei einer **anderen** Gruppe kaputt, warnt `viper` nur; nennt ihr Text den Rechner, bricht es ab, damit er seine Gruppe nicht stillschweigend verliert.
* **Geheimnisse:** Gruppendateien gehören zu den Dateien, deren Rechte `viper preflight` prüft. Tokens gehören nicht hinein.
* **Start:** Besteht die Gruppe noch nicht, legt zuerst der erste Rechner mit NATS Netzwerk und Server an. Sobald sein Server beim Anbieter existiert, starten alle übrigen gleichzeitig; sie warten bis zu 15 Minuten, bis der NATS-Server fertig ist. Besteht die Gruppe schon, wartet niemand auf einen Server. Der erste Rechner je Login-Schlüssel und Firewall legt sie an, die anderen mit denselben warten darauf.
* **Fehler beim Start:** Scheitert der erste Rechner, bevor sein Server da ist, startet kein anderer. Scheitert er danach, werden die laufenden Starts der anderen mit NATS abgebrochen. Bereits angelegte Server bleiben bestehen (wie bei einem einzelnen `viper up`); `viper up --group` wiederholt den Start.
* **Beenden:** Zuerst werden alle Rechner gelöscht, die nicht der NATS-Server sind, gleichzeitig; danach der Server, und mit dem letzten Rechner das Netzwerk. Scheitert oder verweigert einer der ersten (zum Beispiel wegen ungesicherter Arbeit), bleibt der Server. Welcher Rechner der Server ist, sagen die Labels beim Anbieter, nicht die Reihenfolge in der Datei; lässt sich das nicht lesen, löscht `viper` nichts. Rechner im Netzwerk der Gruppe, die nicht in der Datei stehen, beendet `down --group` nicht; dann bleibt der Server stehen, weil er sie noch braucht.
* **Fragen:** Die Sicherheitsabfragen (`DISCARD`, `DELETE`, `CLEANUP`) bleiben je Rechner. Es fragt immer nur ein Rechner zugleich; die anderen warten an ihrer Frage, bis sie an der Reihe sind.

### Claude Code mit den anderen Rechnern der Gruppe sprechen lassen (NATS_MCP)

Mit `NATS_MCP=true` installiert `viper up` auf dem Rechner einen eigenen MCP-Server, `viper-nats-mcp`, und trägt ihn bei Claude Code ein (Name `nats`, Benutzerbereich). Damit kann der Agent auf einem Rechner Nachrichten an die Agenten der anderen Rechner der Gruppe schicken und empfangen. Die Funktion ist optional und standardmäßig aus.

#### Voraussetzung

* Eine NATS-Gruppe (Kapitel „Rechner über NATS verbinden“), `NATS_ENABLED=true`.
* Claude Code ist auf dem Rechner installiert (`viper up` richtet es ein). Ein mit `go build` gebautes `viper` enthält die Binary des MCP-Servers nicht; dann nennt `NATS_MCP_BINARY` eine Datei (`make mcp` baut sie; ein `viper` mit eingebautem Server schreibt sie mit `viper mcp-binary --arch arm64 > datei`), oder bauen Sie `viper` mit `make build`.

#### Vorgehen

1. Tragen Sie in `.env.local`, `machines/<rechner>.env.local` oder die Gruppendatei ein:

   ```
   NATS_MCP=true
   # optional:
   NATS_MCP_AUTOALLOW=read          # off (Standard), read oder all
   NATS_MCP_PUSH_FROM=beta,gamma    # Absender, deren Nachrichten in die Sitzung geschoben werden
   NATS_MCP_TOPICS=build,alerts     # Themen, die der Server beim Start abonniert (in der Gruppendatei: für alle Rechner)
   ```
2. Starten Sie den Rechner mit `viper up` (oder die Gruppe mit `viper up --group`). `viper` kopiert die Binary auf den Rechner (SHA-256 geprüft, sie muss dort laufen, bevor sie die alte ersetzt), schreibt `~/.viper-nats/mcp.env`, ruft `claude mcp add --scope user nats` auf und startet den Server zur Probe über stdio.
3. Prüfen Sie mit `viper check`: Binary, Eintrag bei Claude Code (genau diese Binary), Push-Liste, Standard-Themen (`NATS_MCP_TOPICS`) und erlaubte Werkzeuge, dazu ob die Binary die dieses `viper` ist.
4. Starten Sie Claude Code auf dem Rechner. Die Werkzeuge stehen in jeder Sitzung zur Verfügung (`/mcp` zeigt `nats`). Für Nachrichten, die von selbst erscheinen, siehe „Zwei Wege zum Agenten“.

#### Ergebnis

Der Agent hat diese Werkzeuge:

| Werkzeug | Wirkung |
|---|---|
| `peers` | Maschinen der Gruppe, die sich in den letzten 90 Sekunden gemeldet haben, mit der Version ihres `viper-nats-mcp`, soweit sie sie melden (eine ältere Version meldet sie nicht) |
| `send` | Nachricht an eine Maschine; mit `NATS_OBJECTS` auch mit einer Datei (Parameter `attachment`) |
| `broadcast`, `subscribe`, `unsubscribe` | Rundruf an ein Thema; Themen abonnieren und abbestellen (höchstens 16, die Themen aus `NATS_MCP_TOPICS` mitgezählt; diese sind schon beim Start abonniert und lassen sich nicht abbestellen) |
| `topics` | die Themen: die, die der Agent selbst empfängt (die festen aus `NATS_MCP_TOPICS` sind gekennzeichnet), die, die die anderen Maschinen der Gruppe nach eigener Aussage empfangen, und je Thema die Maschinen, die es empfangen. Eine Maschine, die nichts gesagt hat (ein älteres `viper-nats-mcp`, oder ihre Meldung ist noch nicht da), steht als „has not said which topics“ da und nicht als eine ohne Themen. Die Meldungen kommen mit jedem Lebenszeichen (alle 30 Sekunden) und gleich nach `subscribe` und `unsubscribe` |
| `request`, `reply` | Anfrage mit Warten auf die Antwort (30 Sekunden, höchstens 120; mit `NATS_OBJECTS` auch mit einer Datei); Antwort auf eine Anfrage aus dem Posteingang |
| `fetch` | nur mit `NATS_OBJECTS`: holt die Datei einer Nachricht aus dem Posteingang (die Nummer des Eintrags, in der Ausgabe „handle“) in den Ordner `~/.viper-nats/attachments/` und nennt den Pfad |
| `inbox` | die letzten 200 eingegangenen Nachrichten und Anfragen (jede Textzeile beginnt mit `| `); eine Datei steht in einer eigenen Zeile mit Namen, Art und Größe |

Der Absender einer Nachricht folgt aus dem Subject, das der NATS-Server nur dem Absender selbst erlaubt; ein Agent kann nicht als ein anderer Rechner senden. Anwesend ist ein Rechner nur, solange dort eine Claude-Code-Sitzung läuft (der Server läuft nur mit ihr). Nachrichten an einen Rechner ohne laufende Sitzung gehen verloren; es gibt keine Zwischenspeicherung.

#### Zwei Wege zum Agenten

* **Abholen (Pull):** Der Agent ruft `inbox` auf. Das funktioniert ohne besondere Einstellung, aber der Agent erfährt von einer Nachricht nur, wenn er nachsieht. Soll er das tun, brauchen Sie eine Anweisung (zum Beispiel ein Abschnitt in der `CLAUDE.md` des Repositories); `viper` legt sie nicht an.
* **Schieben (Push):** Nachrichten der Rechner in `NATS_MCP_PUSH_FROM` erscheinen von selbst in der Sitzung. Das geht nur, wenn **Sie** Claude Code selbst und interaktiv so starten:

  ```bash
  claude --dangerously-load-development-channels server:nats
  ```

  `viper` startet Claude nie damit und bestätigt den Warnhinweis nie; der Befehl wird nur angezeigt. In nicht interaktiven Läufen (`claude -p`) wird der Schalter ignoriert. Channels sind eine Vorschau von Claude Code: Schalter, Bestätigung und Freigabe können sich ändern, und auf Team- und Enterprise-Plänen muss ein Administrator sie freischalten. Push ist für den Betrieb mit einem Menschen am Rechner gedacht (zum Beispiel in der tmux-Sitzung), weniger für unbeaufsichtigte Rechner. Es gilt eine Grenze von 10 Nachrichten je Minute und Absender; weitere gehen nur in den Posteingang.

#### Welche Werkzeuge dürfen ohne Nachfrage laufen (`NATS_MCP_AUTOALLOW`)

Claude Code fragt standardmäßig bei jedem Aufruf eines Werkzeugs. `viper` ändert das nur auf Ihren Wunsch und schreibt dann in `~/.claude/settings.json` des Rechners genau die Einträge `mcp__nats__…`; alle anderen Einträge, ein Verweis auf ein Dotfiles-Repository und die Rechte der Datei bleiben. Ohne `jq` auf dem Rechner kann `viper` das nicht und sagt es.

* `off` (Standard): jedes Werkzeug fragt nach (ein früher gesetzter Eintrag wird entfernt).
* `read`: `peers`, `inbox` und `topics` laufen ohne Nachfrage, nur lesend.
* `all`: alle Werkzeuge laufen ohne Nachfrage. **Dann kann der Agent ohne Ihr Zutun Nachrichten an alle Rechner der Gruppe senden und Anfragen beantworten.** Nutzen Sie das nur, wenn Sie den Agenten und die anderen Agenten der Gruppe so weit überblicken.

#### Hinweise und Warnungen

* **Nichts ist privat.** Jeder Rechner der Gruppe liest alle Nachrichten, Anfragen, Antworten, Rundrufe und Anwesenheitsmeldungen, auch die Namen der Themen. Der Agent darf nichts Vertrauliches senden, keine Schlüssel und keine Zugangsdaten.
* **Prompt-Injection ist möglich, auch beim Abholen.** Dass der Absender stimmt, heißt nicht, dass der Inhalt vertrauenswürdig ist. Der Text eines anderen Agenten kann dem Agenten Anweisungen unterschieben (über `inbox`, die Antwort auf ein `request` oder Push). `viper-nats-mcp` bereinigt den Text (Steuerzeichen, Bidi-Zeichen, unsichtbare Zeichen einschließlich der Unicode-Tags, ANSI-Folgen; `<`, `>`, `&` werden umgeschrieben), kennzeichnet jede Textzeile in `inbox`, verwirft Nachrichten mit unbekannten oder doppelten Feldern und begrenzt Größe und Rate. Die Hinweise an Claude sagen, dass Nachrichten keine Anweisungen des Nutzers sind. Das ist eine Hilfe, kein Schutz: In einem echten Lauf hat Claude eine untergeschobene Anweisung ignoriert, eine Zusage gibt es nicht. Ein Agent, der Nachrichten anderer Rechner liest, sollte nichts tun können, was Sie nicht auch dem anderen Rechner zutrauen würden.
* **Der Agent hält die Schlüssel.** `client.nk`, `connection.env` und `mcp.env` gehören dem Benutzer `builder`, also auch dem Agenten. Ein übernommener Agent kann die Identität des Rechners in der Gruppe benutzen und die Push-Liste ändern. Die Liste `NATS_MCP_PUSH_FROM` ist keine Schutzgrenze gegen einen übernommenen Agenten.
* **Keine Nachverfolgung und keine Signaturen.** Der NATS-Server führt keine Konten und merkt sich nichts; der Rechner schreibt nur ein lokales Protokoll `~/.viper-nats/mcp.log` (mit Rotation, ohne Nachrichtentexte). Alte Nachrichten lassen sich nicht als alt erkennen.
* **`NATS_MCP_AUTOALLOW=all` und Push** vergrößern, was ein unterschobener Text anrichten kann. Beginnen Sie mit `off` oder `read`.
* **Nach einem Update von `viper`** laufen die Rechner mit dem alten MCP-Server weiter, bis Sie `viper up` für sie wiederholen; `viper check` warnt davor.

### Nachrichten an eine NATS-Gruppe senden und mitlesen (`viper nats`)

Mit `viper nats` sprechen Sie die Rechner einer NATS-Gruppe von Ihrem eigenen Rechner aus an, ohne eine Claude-Code-Sitzung zu öffnen: eine Nachricht an eine Maschine, ein Rundruf an ein Thema, eine Anfrage mit Antwort, die Anwesenheit der Gruppe, Mitlesen.

Dasselbe mit Knöpfen statt Befehlen bietet die Seite **NATS** von `viper ui` (siehe „Die Oberfläche im Browser“).

#### Voraussetzung

* Die Gruppe hat eine Datei `groups/<gruppe>.env`, die Rechner sind mit `viper up` gestartet (jeder hat eine SSH-Konfiguration), und mindestens einer hat `NATS_ENABLED=true` **und `NATS_MCP=true`** (siehe „Claude Code mit den anderen Rechnern der Gruppe sprechen lassen“). `viper nats` führt dessen MCP-Server `viper-nats-mcp` per SSH auf dem Rechner aus.
* NATS-Gruppen gibt es bei Hetzner, AWS, Google Cloud, Azure, in Kubernetes und mit Docker Compose (Voraussetzungen siehe „Rechner über NATS verbinden“).

#### Vorgehen

```bash
viper nats peers     entwicklung                          # wer hat sich gemeldet (hört 35 Sekunden)
viper nats peers     entwicklung --query                  # schneller (8 Sekunden), siehe Hinweise
viper nats topics    entwicklung [--query]                # erst die festen Themen aus den Einstellungen (NATS_MCP_TOPICS), dann die, die laufende Maschinen abonniert haben
viper nats send      entwicklung "Bitte pushen" --to beta
viper nats broadcast entwicklung "Release um 15 Uhr" --topic news
viper nats request   entwicklung "Wie ist der Stand?" --to beta --timeout 60
viper nats listen    entwicklung [--topic news]           # bis Strg-C; --topic mehrfach oder mit Komma: --topic news,build
viper nats listen    entwicklung --all                    # zusätzlich die Direktnachrichten zwischen den anderen Rechnern
viper nats listen    entwicklung --all --no-presence      # ohne die Meldungen „ich bin da“ (Heartbeat) und die Themenlisten
echo "langer Text" | viper nats send entwicklung - --to beta   # Text von der Standardeingabe
viper nats broadcast entwicklung --topic news --file mitteilung.md   # Text aus einer Datei (--file - liest die Standardeingabe)
```

* **Absender:** Ohne eigenen Schlüssel (siehe „Als Betreiber senden“ unten) kommt Ihre Nachricht von einer **Maschine der Gruppe** an, mit deren Schlüssel und Rechten; `viper` nimmt ohne `--from` die erste Maschine der Gruppe, die startklar ist und nicht der Empfänger ist. Mit `--from <maschine>` wählen Sie sie selbst. Die erste Ausgabezeile nennt den Absender („Sender: alpha …“).
* **Langer Text:** Statt des Textes können Sie bei `send`, `broadcast` und `request` mit `--file DATEI` eine Datei angeben (höchstens 16 KiB; ein abschließender Zeilenumbruch gehört nicht zum Text). Text und `--file` zusammen sind ein Fehler.
* **Dateien:** `--pdf`, `--image`, `--zip` und `viper nats fetch` (Bilder, PDFs und ZIP-Dateien) stehen im Kapitel „Bilder, PDFs und ZIP-Dateien senden“.
* `--json` gibt je Ereignis eine JSON-Zeile aus.
* `--timeout` bei `request` (Standard 30 Sekunden, 1 bis 120) und bei `peers` (wie lange zugehört wird). `topics` zeigt zuerst die festen Themen aus den Einstellungen (`NATS_MCP_TOPICS` in Gruppen- und Rechnerdateien, je Rechner mit `NATS_MCP=true`), ohne dass dort eine Sitzung laufen muss. Danach hört es wie `peers` zu und nennt die Themen, die laufende Maschinen nach eigener Aussage abonniert haben (auch die mit `subscribe` hinzugefügten); eine Maschine ohne laufende Sitzung ist dort, wie bei `peers`, nicht zu sehen. Eine Maschine, die ihre Themen nicht meldet (ein älteres `viper-nats-mcp`, oder die Meldung kommt noch), steht als „has not said“ da, nicht als Maschine ohne Themen.

#### Ergebnis

`send` und `broadcast` melden „sent as <maschine> …“. Das heißt nicht, dass jemand die Nachricht erhalten hat: Es gibt keine Zwischenspeicherung, eine Maschine ohne laufende Claude-Code-Sitzung verpasst sie. `request` gibt den Text der Antwort aus oder endet mit Fehler, wenn nach dem Timeout keine Antwort der **gefragten** Maschine da ist (eine Antwort einer anderen Maschine zählt nicht).

#### Hinweise und Warnungen

* **Als Maschine gesendet, sieht der Empfänger die Maschine als Absender, nicht Sie.** Der Absender folgt aus dem Schlüssel; mit einem eigenen Schlüssel (`viper nats init`) sendet `viper` dagegen als „operator“. Steht die Absender-Maschine in `NATS_MCP_PUSH_FROM` einer Empfänger-Maschine, schiebt deren MCP-Server Ihren Text als Nachricht dieser Maschine in die Sitzung des Agenten. `viper` verweigert das ohne `--yes` und nennt die betroffenen Rechner. Wählen Sie dann eine andere Maschine mit `--from` oder bestätigen Sie mit `--yes`.
* **Der Text ist nicht vertrauenswürdig und nichts ist privat.** Antworten und mitgelesene Nachrichten stammen von Agenten und können Anweisungen enthalten; `viper` entfernt Steuerzeichen, mehr nicht. Senden Sie nichts Vertrauliches.
* **`peers` ohne `--query`** hört 35 Sekunden, weil sich jede Maschine nur alle 30 Sekunden meldet. Mit `--query` bitten Sie alle um eine Meldung (8 Sekunden); dann gilt die gewählte Maschine bei den anderen bis zu 90 Sekunden als anwesend, auch wenn dort kein Agent läuft.
* **Größe:** Ein Text darf höchstens 16 KiB haben, und die kodierte Nachricht höchstens 32 KiB (Steuerzeichen werden länger); sonst meldet `viper` einen Fehler und sendet nichts.
* **Als Betreiber senden (`viper nats init <gruppe>`).** Der Betreiber ist ein eigener Eintrag am NATS-Server mit dem Namen `operator` (einstellbar mit `NATS_OPERATOR_NAME` in der Gruppen- oder Rechnerdatei). `viper nats init entwicklung` erzeugt einen Schlüssel **auf Ihrem Rechner** (`~/.local/state/viper/groups/<gruppe>/operator.nk`, nur für Sie lesbar) und trägt den öffentlichen Teil am Server ein (der Server liest die Konfiguration neu; die Rechner der Gruppe bleiben eingetragen). Danach sendet `viper nats` ohne `--from` als `operator`: es baut einen SSH-Tunnel zum Server auf (über die Servermaschine) und verbindet sich von Ihrem Rechner aus; das Zertifikat holt es bei jedem Aufruf frisch vom Server. Ein Rechner der Gruppe wird nicht mehr als Absender gebraucht. `--from <maschine>` sendet weiter als diese Maschine. Der Empfänger sieht `operator` als Absender. `viper nats revoke <gruppe>` trägt den Betreiber wieder aus und löscht den Schlüssel.
* **Was der Betreiber darf:** senden nur in seinem eigenen Bereich `viper.<gruppe>.operator.>` und alles in der Gruppe mitlesen. Die Anfragebereiche der anderen Rechner (`…req.>`) und den Posteingang `_INBOX` hat er nicht; Antworten auf seine Anfragen kommen als normale Nachrichten an `viper.<gruppe>.<antwortender>.out.operator`. Wer den Schlüssel hat, kann als `operator` senden und alles in der Gruppe lesen (dieselbe Vertraulichkeit wie bei jedem Rechner). **Kein Rechner der Gruppe darf `operator` heißen** (und umgekehrt): `viper up` verweigert ihn, und ein Eintrag überschreibt nie den Schlüssel des anderen Typs.
* **Agenten und der Betreiber:** Für die Agenten ist `operator` ein weiterer Absender, der Anweisungen schickt. `NATS_MCP_PUSH_FROM` darf `operator` nennen, dann schiebt der MCP-Server die Nachrichten des Betreibers von selbst in die Sitzung; Standard ist die leere Liste. Das ist Ihre Entscheidung über das Vertrauen, `viper` warnt dabei nicht.
* **`listen --all`** zeigt zusätzlich die Direktnachrichten zwischen anderen Rechnern der Gruppe, mit Absender und Empfänger (`alpha -> beta: …`; `"to"` bei `--json`). Das ist kein neues Recht: der Server lässt jeden Rechner und den Betreiber die ganze Gruppe lesen. Es gibt in einer Gruppe keine Privatsphäre zwischen den Agenten. `--all` gilt nur für `listen`; `--topic` filtert nur Topic-Nachrichten, Direktnachrichten haben kein Topic.
* **`listen --no-presence`** lässt die Meldungen „ich bin da“ (die Anwesenheit, alle 30 Sekunden, der Heartbeat) und die Themenlisten der Rechner weg. Im Text von `viper nats listen` fehlen sie ohnehin; bei `--json` und in der Oberfläche (Seite NATS, **Mitlesen**) erscheinen sie, und das Kästchen „ohne die Meldungen ‚ich bin da‘ (Heartbeat)“ blendet sie aus.
* **`listen`** beendet das Programm auf der Maschine, wenn die Verbindung abbricht; es kann bis zur nächsten Nachricht der Gruppe (spätestens 30 Sekunden) weiterlaufen.

### Bilder, PDFs und ZIP-Dateien senden (`NATS_OBJECTS`)

Eine Nachricht der Gruppe kann eine Datei tragen: ein Bild (PNG, JPEG, GIF, WebP), ein PDF oder eine ZIP-Datei. Die Datei liegt im **Speicher für Dateien** des NATS-Servers der Gruppe (nicht bei einem Cloud-Anbieter, nicht auf einem eigenen Dienst); die Nachricht nennt sie nur. Der Empfänger holt sie mit dem Werkzeug `fetch` (der Agent) oder mit `viper nats fetch` (Sie, als Betreiber) und liest sie dann aus einer Datei.

#### Voraussetzung

* In der **Gruppendatei** (`groups/<gruppe>.env`) steht `NATS_OBJECTS=true` neben `NATS_ENABLED=true` und `NATS_MCP=true`; die Grenzen sind `NATS_OBJECTS_MAX`, `NATS_OBJECTS_FILE_MAX` und `NATS_OBJECTS_TTL` (siehe Kapitel 4).
* Die Einstellung gilt, wenn der Server der Gruppe **angelegt** wird. Die Einstellungen gehören **nur in die Gruppendatei**; in einer Rechnerdatei werden sie für den Server nicht beachtet. Haben Sie sie später eingeschaltet, meldet `viper check` es, und Sie legen die Gruppe neu an (`viper down --group G`, dann `viper up --group G`). **Das löscht alle Rechner der Gruppe samt ihrem Inhalt.** Alle Rechner der Gruppe brauchen die Version von `viper`, die das kann (`viper up` installiert den passenden `viper-nats-mcp`); eine Maschine mit einer älteren Version lehnt eine Nachricht mit Datei ab, deshalb sendet `viper` Dateien nur an Maschinen, die gemeldet haben, dass sie sie verstehen.
* Auf einer VM liegt der Speicher in `/var/lib/viper-nats`, in einem Pod oder Container im Heimverzeichnis des Servers (dort ein Volume). Der Server muss `NATS_OBJECTS_MAX` plus 1 GiB frei haben; `viper up` prüft es vorher. Erprobt auf Hetzner, in k3d und mit Docker Compose; bei AWS, Google Cloud und Azure läuft derselbe Weg wie bei Hetzner, ist dort aber nicht erprobt.

#### Vorgehen

Von Ihrem Rechner aus (als Maschine der Gruppe oder als Betreiber):

```bash
viper nats send      entwicklung "Bitte ansehen" --to beta --pdf bericht.pdf
viper nats broadcast entwicklung --topic news --image bild.png          # ohne Text
viper nats request   entwicklung "Passt das so?" --to beta --zip projekt.zip
viper nats fetch     entwicklung 0123456789abcdef0123456789abcdef --sha256 <Prüfsumme> --out ./eingang
```

* `--pdf`, `--image` und `--zip` nehmen genau eine Datei; die Art wird an den ersten Bytes der Datei geprüft, nicht an der Endung (`--image` mit einem PDF wird abgelehnt, bevor sich `viper` verbindet). Der Text darf fehlen. Die Datei darf höchstens `NATS_OBJECTS_FILE_MAX` groß sein. Ihr Name erscheint beim Empfänger bereinigt: nur Buchstaben und Ziffern aller Schriften und `. _ - +`, höchstens 64 Bytes, sonst `file`.
* `viper nats listen` zeigt eine Nachricht mit Datei mit einer Zeile `[attachment: …; fetch it with viper nats fetch GROUP <ID> --sha256 <Prüfsumme>]`. Die ID und die Prüfsumme dort brauchen Sie für `viper nats fetch`. **`fetch` gibt es nur mit dem Schlüssel des Betreibers** (`viper nats init`), denn die Datei landet auf Ihrem Rechner; sie wird gegen die Prüfsumme geprüft. In den Ordner `--out` (Standard: der aktuelle) schreibt `viper` nur die Datei `<ID>.<Endung>` und räumt dort nie auf.
* Der Agent sendet mit den Werkzeugen `send`, `broadcast` und `request` und dem Parameter `attachment` (eine Datei im Heimverzeichnis der Maschine). Beim Empfänger zeigt `inbox` die Datei in einer eigenen Zeile, und `fetch` holt sie mit der Nummer des Eintrags („handle“) in `~/.viper-nats/attachments/` (höchstens 50 Dateien und 64 MiB, die ältesten gehen zuerst) und nennt den Pfad. Der Inhalt kommt nie in die Antwort des Werkzeugs, und nichts wird von selbst geholt, geöffnet oder in die Sitzung geschoben.

#### Ergebnis

`viper nats send …` meldet „sent as <maschine> to <ziel> with the file bericht.pdf (pdf, 120 KiB)“. `viper check` auf dem Server meldet, ob der Speicher und der Bucket zu den Einstellungen passen.

#### Hinweise und Warnungen

* **Die Datei ist ungeprüfter Inhalt von einem anderen Agenten.** Ein Bild oder PDF kann Anweisungen enthalten (Text im Bild, versteckter Text im PDF), und ein PDF kann aktive Inhalte haben. Öffnen oder entpacken Sie nichts von Absendern, denen Sie nicht trauen. `viper` führt nichts aus und entpackt nichts.
* **ZIP:** `viper` sieht sich das Archiv an, bevor es gesendet und nachdem es geholt wurde (Inhaltsverzeichnis und, einmal, alle Daten, ohne etwas auf die Platte zu schreiben) und lehnt ab: ein leeres Archiv, mehr als 1000 Einträge, mehr als 64 MiB entpackt oder mehr als das Hundertfache der Dateigröße, Namen mit `..`, absolute Pfade, `C:`, `\`, Namen mit Steuerzeichen, doppelte Namen (auch bei anderer Groß- und Kleinschreibung), überlappende Einträge, Verknüpfungen, verschlüsselte Einträge und andere Verfahren als gespeichert oder deflate. Sehr gut komprimierbare Dateien (zum Beispiel lange Folgen gleicher Zeichen) fallen unter die Grenze von 100 zu 1 und werden abgelehnt. Das Prüfen ist **keine Garantie**: Entpacken Sie ein Archiv nur in einen leeren Ordner und mit einem Werkzeug, das Pfade außerhalb davon verweigert. Ein `.docx`, `.xlsx` oder `.jar` ist technisch eine ZIP-Datei und geht als ZIP durch.
* **Nichts in der Gruppe ist privat, und der Speicher ist keine Ablage.** Jede Maschine der Gruppe und der Betreiber können jede Datei holen. **Jede Maschine kann außerdem Dateien anderer ersetzen, fälschen oder unauffindbar machen, und mit einem besonderen Kopf der Nachricht sogar den ganzen Speicher leeren oder das Holen für alle blockieren (indem sie die zulässigen Lesevorgänge, höchstens 64, belegt)** (der Speicher für Dateien braucht dafür dieselben Rechte, die das Ablegen braucht; sie lassen sich nicht enger fassen). Die Prüfsumme in der Nachricht schützt davor, eine veränderte Datei zu bekommen; gegen das Verschwinden schützt nichts. Heben Sie wichtige Dateien an anderer Stelle auf.
* **Löschen:** Einzelne Dateien lassen sich nicht löschen. Sie verschwinden nach `NATS_OBJECTS_TTL`, alle auf einmal mit einem neuen Server der Gruppe. Die Dateien, die Sie mit `viper nats fetch --out` auf Ihren Rechner geholt haben, räumt `viper` nie auf; der Ordner `~/.viper-nats/attachments/` eines Agenten räumt sich selbst auf.
* **Lebensdauer:** Eine Datei bleibt `NATS_OBJECTS_TTL` Tage (Standard 7), solange die Gruppe läuft. Mit dem Server der Gruppe (`viper down` der Server-Maschine, ein neuer Server, ein neues Volume) ist sie weg; ein Empfänger, der später holt, bekommt „not in the object store“. Ist der Speicher voll, schlägt das Senden mit „the object store of the group is full“ fehl, bis alte Dateien ablaufen.
* **Der Name der Datei ist Text des Absenders.** `viper` bereinigt ihn (siehe oben), zeigt ihn aber nach den Angaben, die es selbst kennt, und kennzeichnet ihn als „untrusted text“; ein Absender kann in 64 Bytes Buchstaben und Leerzeichen trotzdem etwas hineinschreiben, das wie eine Anweisung klingt. Lesen Sie ihn wie den Text der Nachricht.
* **Während einer Sendung mit Datei gilt die sendende Maschine bis zu 90 Sekunden als anwesend:** Der Befehl bittet die anderen, sich zu melden (wie `peers --query`), um zu erfahren, wer Dateien versteht, und meldet sich danach nicht ab. Die Maschinen beantworten eine solche Bitte nur alle 5 Sekunden; zwei Sendungen dicht nacheinander können deshalb bis zu 10 Sekunden warten. Bei einem Rundruf an ein Thema, das noch niemand abonniert hat, gibt es keine Warnung: Die Datei liegt im Speicher, die Nachricht geht an niemanden.
* **Dateien kommen nur an Maschinen, die sie verstehen.** Hat eine Maschine nichts über sich gemeldet (eine ältere Version, oder noch nicht), lehnt `viper` das Senden an sie ab. `viper` wartet bis zu 10 Sekunden auf die Meldungen der Empfänger; bei zwei Sendungen kurz hintereinander kann die zweite so lange warten.
* **Der Speicher ist kein Cloud-Dienst.** Er erscheint bei Hetzner, AWS oder Google nirgends als eigene Ressource; seine Dateien liegen auf der Platte des Servers.

### Ein privates Repository per SSH klonen

#### Voraussetzung

Sie haben im Git-Dienst einen **Deploy-Key** (nur lesend) für das Repository hinterlegt. Die Datei mit dem privaten Schlüssel liegt auf Ihrem lokalen Rechner.

#### Vorgehen

1. Kopieren Sie `machines/example.env.local.example` (legt `viper setup` im Arbeitsverzeichnis an) nach `machines/<rechner>.env.local`.
2. Tragen Sie `REPO_URL` (SSH-Adresse) und `GIT_DEPLOY_KEY_FILE` (Pfad zur Schlüsseldatei) ein.
3. Starten Sie den Rechner mit `viper up <rechner>`.

#### Ergebnis

Das Repository wird geklont. Der Schlüssel liegt auf dem Server unter `~/.ssh/deploy_key` und ist für das Repository als `core.sshCommand` hinterlegt. So funktionieren später auch `git pull` und `git push` (sofern der Schlüssel Schreibrechte hat).

#### Hinweise

* Beim ersten Kontakt mit dem Git-Dienst wird dessen Host-Schlüssel ohne Rückfrage übernommen. Bei HTTPS-Adressen ist kein Schlüssel nötig.
* Ist zusätzlich `VIPER_GH_TOKEN` gesetzt, bleibt es beim Deploy-Key: Die Umschreibung auf HTTPS entfällt dann.
* Ohne `GIT_DEPLOY_KEY_FILE` hat der Server keinen SSH-Schlüssel für den Git-Dienst. Eine SSH-Adresse lehnt der Preflight dann ab. Ausnahme: Mit `VIPER_GH_TOKEN` werden SSH-Adressen von GitHub (bzw. `VIPER_GH_HOST`) automatisch über HTTPS mit dem Token geklont (`git config url.…insteadOf`).
* Der Schlüssel wird bei jedem Start erneut auf den Server kopiert.

### GitHub-CLI und Token auf dem Rechner nutzen

Auf jedem Rechner ist die GitHub-CLI `gh` installiert. Mit einem Personal Access Token (PAT) können Claude Code und Sie damit Pull Requests anlegen, Issues lesen und per HTTPS pushen.

#### Voraussetzung

Sie haben ein GitHub-Konto mit Schreibrecht auf das Repository des Rechners.

#### Vorgehen

1. Legen Sie bei GitHub ein **fein granulares** Token an: **Settings → Developer settings → Personal access tokens → Fine-grained tokens → Generate new token**.
   * **Token name:** frei wählbar, zum Beispiel `viper-<repo>`. Nehmen Sie ein eigenes Token pro Rechner.
   * **Resource owner:** das Konto oder die Organisation, der das Repository gehört. Eine Organisation muss das Token unter Umständen erst freigeben; bis dahin steht es auf „pending“.
   * **Expiration:** kurz, zum Beispiel 30 oder 90 Tage.
   * **Repository access:** **Only select repositories** und das Repository des Rechners. „Public repositories“ erlaubt nur Lesen und reicht nicht.
   * **Permissions:** unter **Repository permissions** die Rechte aus den Tabellen unten. **Account permissions** braucht es nicht.
2. Tragen Sie das Token (es beginnt mit `github_pat_`) als `VIPER_GH_TOKEN` ein:
   * für einen einzelnen Rechner in `machines/<rechner>.env.local` (empfohlen), oder
   * für alle Rechner in `.env.local` oder als `export VIPER_GH_TOKEN=...` in der Shell.

   Für GitHub Enterprise setzen Sie zusätzlich `VIPER_GH_HOST` auf den Hostnamen.
3. Prüfen Sie vorab: `viper preflight <rechner> --repo <url>`. Unter `== GitHub` stehen dann `VIPER_GH_TOKEN valid for …`, `token can access <konto>/<repo>` und `token can push to <konto>/<repo>`.
4. Starten Sie den Rechner mit `viper up <rechner>` bzw. `viper up <rechner>`.

Diese Rechte braucht das Token immer:

| Berechtigung | Stufe | Wofür |
|---|---|---|
| **Contents** | Read and write | Klonen, Pullen und Pushen per HTTPS |
| **Pull requests** | Read and write | `gh pr create`, `gh pr view`, Kommentare, Mergen |
| **Metadata** | Read-only | Pflicht, GitHub setzt es selbst |

Diese Rechte vergeben Sie nur bei Bedarf:

| Berechtigung | Stufe | Wofür |
|---|---|---|
| Issues | Read and write (nur lesen: Read-only) | `gh issue …`, Kommentare an Issues |
| Actions | Read-only | `gh run list`, `gh run view`: Ergebnisse der CI ansehen |
| Commit statuses | Read-only | Status einzelner Commits lesen |
| Workflows | Read and write | Änderungen an Dateien unter `.github/workflows/` pushen; ohne dieses Recht lehnt GitHub solche Pushes ab |

Alle anderen Berechtigungen bleiben auf „No access“. Die Liste der Berechtigungen je API-Endpunkt finden Sie bei GitHub unter „Permissions required for fine-grained personal access tokens“; Git über HTTPS steht dort nicht, dafür gilt **Contents**.

Ein klassisches Token braucht die Scopes `repo` und `read:org` (für Workflow-Dateien zusätzlich `workflow`). Es gilt für alle Repositories Ihres Kontos; nehmen Sie deshalb besser ein fein granulares.

#### Ergebnis

Die Meldung `gh: logged in to github.com` erscheint. Auf dem Server ist `gh` angemeldet, und `git` nutzt das Token für HTTPS-Adressen. Private Repositories können Sie daher auch per HTTPS-URL klonen lassen.

#### Hinweise

* Commits auf dem Server brauchen einen Autor. Setzen Sie `VIPER_GIT_USER_NAME` und `VIPER_GIT_USER_EMAIL`, oder lassen Sie beides weg: Mit `VIPER_GH_TOKEN` übernimmt `viper up` den Namen des GitHub-Kontos (sonst den Anmeldenamen) und dessen `noreply`-Adresse (`<id>+<login>@users.noreply.github.com`, nur bei github.com), sodass Ihre private Adresse nicht in den Commits steht. Eigene Werte haben Vorrang. Was auf dem Server schon eingetragen ist, ersetzen abgeleitete Werte nicht. Ohne beides warnt `viper up`, und `viper check` zeigt die Warnung, weil Commits dann scheitern.
* `viper` liest bewusst nicht `GH_TOKEN`. Ein Token, das Sie für Ihr lokales `gh` exportiert haben, landet so nie ungewollt auf einem Server.
* Mit **Contents: Read and write** darf der Agent auf jeden Branch pushen, auch direkt auf `main`, und er darf Force-Pushes ausführen und Branches löschen. Weitere Einstellungen sind dafür nicht nötig. Soll er nur über Pull Requests arbeiten, schützen Sie `main` im Repository unter **Settings → Rules → Rulesets**, etwa mit „Require a pull request before merging“, „Block force pushes“ und „Restrict deletions“. Bei privaten Repositories gibt es Rulesets erst ab GitHub Pro bzw. Team.
* Der Preflight prüft das Schreibrecht, ohne etwas zu schreiben: Er ruft die Liste der Referenzen ab, mit der jeder `git push` beginnt. Das Recht **Workflows** und Regeln für einzelne Branches prüft er nicht.
* Das Token wird per SSH über die Standardeingabe übertragen. Es steht weder in den Hetzner-User-Data noch in einer Befehlszeile.
* Auf dem Server liegt das Token unverschlüsselt in `~/.config/gh/hosts.yml`. Claude Code und jeder Prozess des Benutzers `builder` können es lesen. Vergeben Sie deshalb nur die Rechte, die der Agent braucht.
* Mit dem Löschen des Servers ist das Token dort entfernt. Bei GitHub bleibt es gültig, bis es abläuft oder Sie es widerrufen.

### Claude Code ohne erneute Anmeldung nutzen

Ohne weitere Einstellung melden Sie sich nach jedem neuen Server auf dem Rechner bei Claude an (`claude`, dann `/login`). Mit einem langlebigen Token Ihres Claude-Abos (Pro, Max, Team oder Enterprise) erledigt `viper up` das für Sie. Die Nutzung auf dem Server zählt dann zu Ihrem Abo, nicht zu API-Guthaben.

#### Voraussetzung

Sie haben ein Claude-Abo und auf einem Rechner mit Browser Claude Code installiert.

#### Vorgehen

1. Erzeugen Sie das Token einmalig in **Ihrem eigenen Terminal**: `claude setup-token`. Folgen Sie der Anmeldung im Browser und kopieren Sie das angezeigte Token.
2. Tragen Sie es als `VIPER_CLAUDE_TOKEN=…` ein, für einen Rechner in `machines/<rechner>.env.local` oder für alle in `.env.local`. Achten Sie darauf, dass die Datei die Rechte `600` hat.
3. Starten Sie den Rechner mit `viper up <rechner>`. Bei einem laufenden Rechner reicht derselbe Befehl, er überträgt das Token neu.

#### Ergebnis

Die Meldung `claude: logged in with VIPER_CLAUDE_TOKEN` erscheint. `claude` ist auf dem Server in SSH-Befehlen, in tmux und im VS-Code-Terminal angemeldet. In Claude Code zeigt `/status` im Tab **Status** dann `Auth token: CLAUDE_CODE_OAUTH_TOKEN`. Prüfen können Sie das mit `viper check <rechner>` oder direkt mit `viper ssh <rechner> --cmd "bash -lc 'claude -p \"Antworte nur mit OK\" </dev/null'"`. Das `</dev/null` ist nötig, weil `claude -p` sonst auf weitere Eingaben wartet.

#### Hinweise

* Das Token wird per SSH über die Standardeingabe übertragen. Es steht weder in den Hetzner-User-Data noch in einer Befehlszeile. Auf dem Server liegt es in `~/.config/viper/claude.env` (Rechte `600`), geladen von einer Zeile am Anfang von `~/.bashrc` und `~/.profile`.
* Claude Code und jeder Prozess des Benutzers `builder` können das Token lesen. Es gilt ein Jahr und gibt Zugriff auf Ihr Abo. Behandeln Sie es wie ein Passwort und geben Sie es nicht in Chats oder Tickets weiter.
* `viper` liest bewusst nicht `CLAUDE_CODE_OAUTH_TOKEN`. Ein lokal gesetztes Token landet so nicht ungewollt auf einem Server.
* Das Token kann nur Modell-Anfragen stellen. Remote-Control-Sitzungen und claude.ai-Connectors gehen damit nicht, lokal konfigurierte MCP-Server schon. `claude --bare` liest das Token nicht.
* Setzen Sie auf dem Server kein `ANTHROPIC_API_KEY`: Ein API-Key hat Vorrang vor dem Token und würde über API-Guthaben abrechnen.
* Ist das Token abgelaufen, erzeugen Sie mit `claude setup-token` ein neues, tragen es ein und führen `viper up <rechner>` erneut aus.

### Andere Modelle nutzen (Ollama, vLLM, Gateway)

Statt der Anthropic-Modelle kann Claude Code auf dem Server ein anderes Modell nutzen, das über eine Anthropic-kompatible Schnittstelle (Messages API) erreichbar ist. Beispiele sind Ollamas Cloud-Modelle, ein eigener vLLM-Server (etwa auf RunPod) oder ein LLM-Gateway.

#### Voraussetzung

* Sie haben Zugang zu einem solchen Dienst und einen API-Schlüssel dafür. Bei Ollama erzeugen Sie ihn unter `https://ollama.com/settings/keys`.
* `VIPER_CLAUDE_TOKEN` ist für diesen Rechner nicht gesetzt. Beides zusammen lehnt der Preflight ab, weil der API-Schlüssel sonst Vorrang vor dem Abo hätte.

#### Vorgehen

1. Tragen Sie in `machines/<rechner>.env.local` ein (Werte nicht in Chats oder Tickets kopieren):
   ```
   VIPER_ANTHROPIC_BASE_URL=https://ollama.com
   VIPER_ANTHROPIC_AUTH_TOKEN=<Ihr Ollama-API-Schlüssel>
   VIPER_CLAUDE_MODEL=<Modellname>
   ```
   Für einen vLLM-Server setzen Sie dessen Adresse (zum Beispiel `https://<pod-id>-8000.proxy.runpod.net`), den vLLM-API-Schlüssel und den dort ausgelieferten Modellnamen (`--served-model-name`).
2. Optional: `VIPER_CLAUDE_MAX_CONTEXT_TOKENS=<Tokens>` setzt das Kontextfenster, das Claude Code für das Modell annimmt (sonst 200.000), etwa `1048576` für 1M. Ohne diese Angabe zeigt Claude Code beim Start die Warnung `unrecognized_model`; sie ist harmlos und bedeutet nur, dass es 200.000 Tokens annimmt.
3. Prüfen Sie vorab: `viper preflight <rechner>`. Der Preflight stellt dem Modell eine kurze Anfrage, genau wie Claude Code sie stellt, und meldet einen falschen Schlüssel oder Modellnamen.
4. Starten Sie den Rechner: `viper up <rechner>`. Bei einem laufenden Rechner reicht derselbe Befehl.

#### Ergebnis

Die Meldung `claude: using model <Modell> at <Adresse>` erscheint. `claude` nutzt auf dem Server dieses Modell, auch für Hintergrundaufgaben und Subagents. `viper check <rechner>` zeigt `model <Modell> at <Adresse> answers`.

#### Hinweise

* **Modellnamen:** Über `https://ollama.com` heißen Cloud-Modelle ohne das Suffix `:cloud`, das ein lokales Ollama verwendet. Welcher Name stimmt, zeigt der Preflight. Ein Suffix wie `[1m]` ist eine Markierung von Claude Code und gehört nicht zum Modellnamen; für Fremdmodelle setzen Sie das Fenster besser mit `VIPER_CLAUDE_MAX_CONTEXT_TOKENS`.
* **Abrechnung** erfolgt beim Anbieter des Modells, nicht über Ihr Claude-Abo.
* **Einschränkungen:** Das Modell muss Tools (Funktionsaufrufe) beherrschen. Laut Ollama funktionieren gehostete Websuche und manche erweiterten Tool-Funktionen nicht vollständig. Remote Control ist mit einer anderen Adresse als `api.anthropic.com` abgeschaltet. Qualität und Verhalten bei langen Aufgaben hängen stark vom Modell ab.
* Der Schlüssel wird wie das Abo-Token per SSH über die Standardeingabe übertragen und liegt auf dem Server in `~/.config/viper/claude.env` (Rechte `600`). Claude Code und jeder Prozess von `builder` können ihn lesen.
* Wechseln Sie zurück zum Abo, entfernen Sie die drei Einstellungen, setzen Sie `VIPER_CLAUDE_TOKEN` und führen Sie `viper up <rechner>` erneut aus.

### Werkzeuge mit Homebrew installieren

Swap: Mit `VIPER_SWAP` legt `viper up` eine Swap-Datei `/swapfile` an. Ein Faktor richtet sich nach dem RAM des Servers: `1` ergibt bei einem Server mit 8 GB RAM 8 GB Swap, `0.5` ergibt 4 GB. Eine Größe wie `4G` gilt unabhängig vom RAM. Die Swap ersetzt keinen Arbeitsspeicher, sie fängt Spitzen ab (etwa bei `docker build` oder `npm install`), statt dass der Prozess beendet wird. Der Kernel nutzt sie nur im Notfall (`vm.swappiness=10`), und sie bleibt nach einem Neustart aktiv. Ein erneutes `viper up` ändert eine vorhandene Swap nicht; `viper check` meldet, ob sie aktiv ist. Scheitert das Anlegen, warnt `viper up` nur.

Auf jedem Rechner ist Homebrew (`brew`) installiert, sofern Sie es nicht mit `VIPER_HOMEBREW=0` abschalten. Was Sie von Hand installieren, verschwindet mit dem Server. Damit jeder neue Rechner dieselben Werkzeuge bekommt, tragen Sie sie in die Einstellungen oder in eine `Brewfile` im Repository ein.

#### Begriffe

* **Tap:** eine zusätzliche Paketquelle, meist ein GitHub-Repository `owner/homebrew-repo`, angesprochen als `owner/repo`. Ohne Tap stehen nur die Pakete des Hauptkatalogs zur Verfügung.
* **Trust:** Homebrew lädt Formeln, Casks und Befehle aus fremden Taps nur, wenn Sie ihnen vertrauen (`brew trust`). Vertrauen können Sie einem ganzen Tap oder einzelnen Formeln.
* **Formel (Tool):** das eigentliche Paket, etwa `ripgrep`, oder aus einem Tap `owner/repo/name`.

#### Vorgehen mit Einstellungen

1. Tragen Sie in `machines/<rechner>.env.local` (oder für alle Rechner in `.env.local`) ein. Jede Einstellung ist eine Liste: Einträge trennen Sie mit Leerzeichen oder Zeilenumbrüchen, und den ganzen Wert setzen Sie in doppelte Anführungszeichen (sonst bricht das Einlesen der Datei ab):
   ```
   VIPER_BREW_TAPS="hashicorp/tap"
   VIPER_BREW_PACKAGES="ripgrep fd hashicorp/tap/terraform"
   ```
   Lange Listen dürfen über mehrere Zeilen gehen:
   ```
   VIPER_BREW_PACKAGES="
     ripgrep
     fd
     hashicorp/tap/terraform
   "
   ```
   Kommas sind keine Trenner; der Preflight meldet einen Eintrag wie `ripgrep,fd` als ungültig.
   * `VIPER_BREW_TAPS`: `owner/repo` für einen Tap auf GitHub, `owner/repo=URL` für einen Tap anderswo (z. B. `meinefirma/tools=https://git.example.com/tools.git`). Zugangsdaten in der URL (`https://user:token@…`) lehnt der Preflight ab, weil sie auf dem Server und in den Protokollen landen würden.
   * `VIPER_BREW_TRUST`: `owner/repo` vertraut dem ganzen Tap, `owner/repo/name` einer einzelnen Formel. Mit `cask:` oder `command:` davor vertrauen Sie einem Cask oder einem externen Befehl. Meist ist das nicht nötig: Wer `owner/repo/name` installiert, vertraut damit genau dieser Formel. Vertrauen Sie so wenig wie möglich, also lieber einer Formel als einem ganzen Tap. `brew trust` nimmt auch Tippfehler an; `viper check` meldet einen vertrauten, aber nicht eingebundenen Tap als `untapped:…`.
   * `VIPER_BREW_PACKAGES`: `name` aus dem Hauptkatalog oder `owner/repo/name` aus einem Tap.
2. Prüfen Sie vorab mit `viper preflight <rechner>`. Er meldet Einträge im falschen Format.
3. Starten Sie den Rechner mit `viper up <rechner>`. `viper up` führt die Schritte in der Reihenfolge Tap, Trust, Installation aus und meldet jeden Schritt (`brew: tap …`, `brew: trusted …`, `brew: installed …`).

#### Vorgehen mit einer Brewfile im Repository

Eine `Brewfile` wird nur angewendet, wenn Sie `VIPER_BREWFILE=1` setzen. Sie ist Ruby-Code aus dem Repository, den `brew bundle` ausführt; ihre `trusted:`-Angaben bleiben dauerhaft in Homebrews Trust-Speicher, und sie kann auch andere Paketarten installieren. Setzen Sie die Option deshalb nur für Repositories, deren Inhalt Sie vertrauen. Der Preflight weist mit `WARN` darauf hin.

Legen Sie im Repository eine Datei `Brewfile` an. Homebrew unterscheidet dort selbst zwischen Taps und Formeln, und der Trust steht direkt am Tap:
```ruby
tap "hashicorp/tap", trusted: true
brew "ripgrep"
brew "hashicorp/tap/terraform"
```
Statt dem ganzen Tap können Sie einzelnen Formeln vertrauen: `tap "hashicorp/tap", trusted: { formulae: ["terraform"] }`. Mit `VIPER_BREWFILE=1` führt `viper up` nach dem Klonen `brew bundle install --no-upgrade` mit dieser Datei aus, und `viper check` prüft sie mit `brew bundle check`. So bekommt jeder Rechner dieses Projekts dieselben Werkzeuge, versioniert mit dem Code.

#### Ergebnis

Die Werkzeuge stehen in allen Shells zur Verfügung, auch für Claude Code. `viper check` meldet `Homebrew: taps, trust entries and formulae present` bzw. `Brewfile of the repository satisfied`, oder was fehlt.

#### Hinweise

* Schritte, die scheitern, brechen die Einrichtung nicht ab. `viper up` warnt, das Protokoll liegt auf dem Server in `~/.config/viper/brew-packages.log`, und ein erneuter `viper up` versucht es wieder.
* Jedes Paket verlängert die Einrichtung. Manche Formeln baut Homebrew aus dem Quellcode, das dauert auf kleinen Servern spürbar.
* Homebrew wird dabei nicht selbst aktualisiert, und vorhandene Pakete werden nicht aktualisiert (`--no-upgrade`).
* Vertrauen Sie nur Taps, deren Herausgeber Sie vertrauen: Formeln sind Ruby-Code, den Homebrew beim Installieren ausführt.
* Von Hand geht es jederzeit auch: `viper ssh <rechner> --cmd "bash -lc 'brew install <name>'"`.

### VS Code im Browser nutzen (code-server)

Auf Wunsch läuft auf dem Rechner `code-server`, VS Code im Browser. Das ist nützlich, wo es kein Remote-SSH gibt, etwa auf dem iPad. Der Dienst ist **nicht** aus dem Internet erreichbar: Er lauscht nur auf `127.0.0.1:8080` des Servers, und die Firewall lässt weiterhin nur SSH zu. Sie erreichen ihn über einen SSH-Tunnel.

#### Voraussetzung

Der Rechner wird neu angelegt oder mit `viper up` erneut eingerichtet. Ein SSH-Client mit Port-Weiterleitung ist vorhanden (auf dem iPad zum Beispiel Termius oder Blink).

#### Vorgehen

1. Tragen Sie in `machines/<rechner>.env.local` ein (der Wert steht in Anführungszeichen, wenn er Sonderzeichen enthält):

   ```
   VIPER_CODE_SERVER=1
   VIPER_CODE_SERVER_PASSWORD="ein-langes-passwort"
   ```

   Das Passwort hat mindestens 12 Zeichen, ohne Leerzeichen und Zeilenumbrüche.
2. Prüfen Sie vorab: `viper preflight <rechner>`.
3. Starten Sie den Rechner: `viper up <rechner>`.
4. Bauen Sie den Tunnel auf: `ssh -L 8080:127.0.0.1:8080 hetzner-claude-<rechner>`. Das Fenster bleibt offen, solange Sie arbeiten.
5. Öffnen Sie im Browser `http://127.0.0.1:8080` und melden Sie sich mit dem Passwort an.

#### Ergebnis

`viper up` meldet `code-server: running on the server at 127.0.0.1:8080`, und `viper check <rechner>` meldet `ok    code-server … on 127.0.0.1:8080`. Im Browser öffnet sich VS Code auf dem Server.

#### Hinweise

* Installiert wird mit dem offiziellen Installationsskript von `code-server` (ein `.deb`, per `sudo`), als systemd-Dienst `code-server@builder`. Scheitert das, warnt `viper up` nur; ein erneutes `viper up` versucht es wieder. Das Protokoll liegt auf dem Server unter `~/.config/viper/code-server-install.log`.
* Das Passwort geht über die Standardeingabe in `~/.config/code-server/config.yaml` (Rechte `600`). Es steht weder in den User-Data noch in einer Befehlszeile noch in den Protokollen. Ändern Sie es in der Datei und führen Sie `viper up` erneut aus, startet der Dienst mit dem neuen Passwort.
* Die Adresse ist `http`, nicht `https`. Das ist ungefährlich, weil sie nur durch den SSH-Tunnel läuft. Öffnen Sie den Port nie in der Firewall des Anbieters.
* Wer Zugriff auf `code-server` hat, hat ein Terminal als Benutzer `builder`, also Root-Rechte auf dem Server. Behandeln Sie das Passwort wie einen SSH-Schlüssel.
* Mit dem Rechner wird auch `code-server` gelöscht.

#### Von einem anderen Rechner aus

Auf dem Server ändern Sie nichts: `code-server` bleibt auf `127.0.0.1`, und die Firewall lässt nur SSH zu. Der andere Rechner braucht:

1. **Zugang per SSH.** Er braucht den Login-Schlüssel, mit dem der Server angelegt wurde (`~/.ssh/hetzner-build`), oder einen eigenen Schlüssel. Einen eigenen Schlüssel tragen Sie beim Anlegen über `IPAD_SSH_PUBLIC_KEY_FILE` ein oder später in `~/.ssh/authorized_keys` des Benutzers `builder`.
2. **Das Passwort** aus `VIPER_CODE_SERVER_PASSWORD` (auf dem Server in `~/.config/code-server/config.yaml`).
3. **Einen Tunnel mit dem DNS-Namen.** Der Alias `hetzner-claude-<rechner>` existiert nur auf dem Rechner, der den Server angelegt hat. Verbinden Sie sich dort mit:

   ```
   ssh -L 8080:127.0.0.1:8080 builder@<SERVER_PREFIX>-<rechner>.<DNS_DOMAIN>
   ```

   Öffnen Sie dann `http://127.0.0.1:8080` im Browser dieses Rechners.

Beim ersten Verbinden kennt der andere Rechner den Server noch nicht. Ist dort die Host-CA eingetragen (`viper setup` zeigt den öffentlichen Schlüssel; Eintrag `@cert-authority *.<DNS_DOMAIN> <Schlüssel>` in `known_hosts`), vertraut er ihm ohne Rückfrage; sonst bestätigen Sie den Fingerabdruck von Hand. Belegt der Rechner Port 8080 schon, nehmen Sie einen anderen lokalen Port, etwa `-L 9090:127.0.0.1:8080`, und öffnen `http://127.0.0.1:9090`.

Öffnen Sie den Port 8080 nie in der Firewall des Anbieters und binden Sie `code-server` nicht an `0.0.0.0`: Sonst wäre ein Terminal mit Root-Rechten nur durch ein Passwort ohne Verschlüsselung geschützt.

### Vom iPad aus verbinden

#### Vorgehen

1. Erzeugen Sie auf dem iPad ein SSH-Schlüsselpaar und speichern Sie den öffentlichen Schlüssel als Datei auf Ihrem lokalen Rechner.
2. Tragen Sie den Pfad bei `IPAD_SSH_PUBLIC_KEY_FILE` in `.env.local` ein.
3. Starten Sie einen neuen Rechner. Der Schlüssel wird für den Benutzer `builder` hinterlegt.
4. Verbinden Sie sich mit Benutzer `builder` und dem DNS-Namen des Rechners. `IPAD_SSH_PUBLIC_KEY_FILE` muss der **öffentliche** Schlüssel sein (`.pub`); einen privaten lehnt der Preflight ab, weil die Datei in die User-Data des Servers kommt.

#### Hinweise

* Bei einem bereits laufenden Server tragen Sie den Schlüssel selbst in `/home/builder/.ssh/authorized_keys` ein.
* Die Prüfung des Server-Zertifikats funktioniert nur, wenn die iPad-App Host-Zertifikate unterstützt. Die öffentliche Host-CA gibt `viper setup` am Ende aus.
* Erlaubt die Firewall Ihr Netz nicht, kommen Sie nicht durch (siehe Kapitel 4, Firewall).

### Einen Rechner beenden

#### Voraussetzung

Schließen Sie alle VS-Code-Fenster und Terminals, die auf dem Rechner geöffnet sind, und beenden Sie die tmux-Sitzungen.

#### Vorgehen

1. Führen Sie `viper down <rechner>` aus.
2. Lesen Sie die Ausgabe: `viper down` zeigt je Repository und je Worktree den Git-Stand, Commits, die auf keinem Remote liegen (auf allen lokalen Branches), und die tmux-Sitzungen. Es sucht Repositories in allen Arbeitsverzeichnissen, mit denen der Rechner je gestartet wurde (auch wenn Sie `WORKSPACE_DIR` später geändert haben oder den Rechner von einem anderen Computer aus gestartet haben; der Server führt die Liste selbst mit), im Home-Verzeichnis von `builder` und in `/tmp` und `/var/tmp`. Dateien und Ordner direkt im Arbeitsverzeichnis, die zu keinem Repository gehören, listet es unter **Not in any git repository (lost)** auf. Dateien außerhalb von Repositories im Home-Verzeichnis oder in `/tmp` prüft es nicht. Neue Dateien findet es unabhängig von Git-Einstellungen wie `status.showUntrackedFiles=no`; Dateien, die per `assume-unchanged` oder `skip-worktree` aus `git status` ausgeblendet sind, meldet es eigens, ebenso Commits, die nur über einen Tag erreichbar sind, der auf dem Server neu angelegt wurde (Tags, die mit dem Clone kamen oder auf einem Remote liegen, zählen nicht). Bricht die Prüfung auf dem Server vorzeitig ab, löscht es nichts. Liegt ein Remote eines Repositorys auf dem Server selbst (etwa ein lokales Bare-Repository), meldet es auch das, weil dorthin gepushte Commits mit dem Server verloren gehen. Gibt es ungesicherte Arbeit (uncommittete Änderungen, Commits auf keinem Remote, mit `git stash` beiseitegelegte Änderungen, Dateien außerhalb eines Repositorys, ein fehlendes Arbeitsverzeichnis oder ein nicht lesbares Repository), steht `UNSAVED WORK` in der Ausgabe, und Sie müssen zusätzlich `DISCARD` eingeben, um fortzufahren. Von Git ignorierte Dateien (zum Beispiel `.env` oder Build-Verzeichnisse) werden unter **Ignored by git (also lost)** aufgelistet, blockieren aber nicht.
3. Sichern Sie offene Änderungen (committen und pushen), falls nötig, und starten Sie `viper down` erneut.
4. Geben Sie zur Bestätigung `DELETE` ein.

#### Ergebnis

Server, DNS-Eintrag und die lokale SSH-Konfiguration des Rechners sind gelöscht. Alle Dateien auf dem Server sind weg. Beim nächsten `viper up <rechner>` wird das Repository neu geklont.

#### Hinweise

* Auch Ihre Claude-Anmeldung und alle nicht gepushten Änderungen gehen verloren.
* `viper down` bricht ab, wenn Sie `DELETE` nicht eingeben.
* Hat `viper up` für den Rechner einen k3d-Cluster angelegt und ist er der letzte darin, geht der Cluster mit (die Abfrage nennt das). Ob eine von `viper` angelegte k3d-Registry mitgelöscht wird, fragt `viper down` danach; `--delete-registry` und `--keep-registry` beantworten es vorab (siehe „Registry und Cluster von `viper up` anlegen lassen“).
* Findet `viper down` den Server beim Anbieter nicht (etwa weil Sie ihn in dessen Konsole gelöscht haben), räumt es SSH-Konfiguration, hinterlegten Schlüssel und DNS-Eintrag erst nach Eingabe von `CLEANUP` auf. So richtet ein Token für das falsche Projekt keinen Schaden an. Den DNS-Eintrag löscht es nur, wenn er noch auf die zuletzt bekannte IP-Adresse zeigt.

### Einen nicht erreichbaren Rechner löschen

#### Vorgehen

Führen Sie `viper down <rechner> --force` aus und bestätigen Sie mit `DELETE`.

#### Hinweise

Mit `--force` entfällt die Git-Prüfung. Nicht gepushte Änderungen gehen verloren. Nutzen Sie das nur, wenn SSH nicht erreichbar ist.

## 4. Einstellungen

Die Einstellungen stehen in `.env.local`. Für einzelne Rechner können Sie sie in `machines/<rechner>.env.local` überschreiben.

| Einstellung | Bedeutung | Standard |
|---|---|---|
| `SERVER_PREFIX` | Namensanfang aller Ressourcen | `claude` |
| `PROVIDER` | Anbieter: `hetzner`, `aws`, `gcp`, `azure`, `kubernetes` oder `compose`; wird nur aus den Einstellungsdateien gelesen, nicht aus der Shell | `hetzner` |
| `SERVER_TYPE` | Servertyp (Hetzner), Instanztyp (AWS), Maschinentyp (GCP) bzw. VM-Größe (Azure) (Kubernetes und Docker Compose: `<CPUs>x<GiB>`, etwa `2x4`) | `cpx32`, `t3.large`, `e2-standard-2`, `Standard_D2s_v5` bzw. `2x4` (Kubernetes, Docker Compose) |
| `SERVER_LOCATION` | Standort (Hetzner), Region oder Zone (AWS, z. B. `eu-central-1b`), Zone (GCP, z. B. `europe-west3-b`) bzw. Region (Azure, z. B. `germanywestcentral`) (Kubernetes: der Kontext der `kubeconfig`, Pflicht; Docker Compose: der Docker-Kontext, `default` ist der lokale Daemon, Pflicht) | `fsn1`, `eu-central-1`, `europe-west3-b` bzw. `germanywestcentral` |
| `SERVER_IMAGE` | Betriebssystem-Image (bei Kubernetes und Docker Compose nicht benutzt: dort gilt `K8S_IMAGE` bzw. `COMPOSE_IMAGE`); bei AWS, GCP und Azure `ubuntu-<Version>` (AWS auch `ami-…`, GCP auch `projects/…`, Azure auch `Herausgeber:Angebot:SKU:Version`) | `ubuntu-26.04` (Hetzner), sonst `ubuntu-24.04` |
| `AWS_PROFILE` | AWS-Profil für die Anmeldung (nur AWS) | Standardkette |
| `AWS_SUBNET_ID`, `AWS_VPC_ID` | Subnetz bzw. VPC für den Server (nur AWS) | Default-VPC |
| `AWS_ROOT_GB` | Größe der Systemplatte in GB (nur AWS) | `50` |
| `AWS_TAGS` | Zusätzliche Tags für Instanz und Systemplatte als kommagetrennte `SCHLÜSSEL=WERT`-Paare, z. B. `owner=me,ttl=2026-11-01T12:00:00Z` (nur AWS) | leer |
| `AWS_TAGS_SHARED` | Zusätzliche Tags für Schlüsselpaar und Security Group, gleiches Format wie `AWS_TAGS` (nur AWS) | leer |
| `GCP_PROJECT` | Google-Cloud-Projekt (nur GCP, Pflichtangabe) | leer |
| `GCP_NETWORK`, `GCP_ROOT_GB` | VPC-Netz bzw. Größe der Bootplatte in GB (nur GCP) | `default` bzw. `50` |
| `AZURE_SUBSCRIPTION_ID` | Azure-Abonnement (nur Azure, Pflichtangabe) | leer |
| `AZURE_RESOURCE_GROUP`, `AZURE_SUBNET_ID`, `AZURE_ROOT_GB` | Ressourcengruppe, vorhandenes Subnetz bzw. Größe der Systemplatte in GB (nur Azure) | `viper`, eigenes Netz bzw. `64` |
| `K8S_IMAGE` | Image der Rechner (nur `kubernetes`; `viper image build` baut es) | `ghcr.io/pt9912/viper-machine:<Version>` (das veröffentlichte Image zu Ihrer Version von `viper`) |
| `COMPOSE_IMAGE` | Image der Rechner (nur `compose`; `viper image build` baut es) | `ghcr.io/pt9912/viper-machine:<Version>` (das veröffentlichte Image zu Ihrer Version von `viper`) |
| `COMPOSE_DOCKER`, `COMPOSE_DOCKER_HOST`, `COMPOSE_DOCKER_IMAGE` | Docker auf dem Rechner: `dind` (privilegierter zweiter Container), `remote` (Daemon unter `COMPOSE_DOCKER_HOST`) oder `none`; Image des zweiten Containers (nur `compose`) | `dind`, leer, `docker:dind` |
| `K8S_NAMESPACE`, `K8S_STORAGE_CLASS`, `K8S_DISK_GB`, `K8S_BREW_GB` | Namespace, StorageClass, Größe des Volumes für das Heimverzeichnis und Größe des Volumes für Homebrew in GB (nur `kubernetes`) | `viper`, die des Clusters, `20` bzw. `10` |
| `K3D_REGISTRY`, `K3D_CLUSTER_CREATE`, `K3D_AGENTS` | k3d (`SERVER_LOCATION=k3d-<cluster>`): Name der Registry, die `viper up` anlegt und mit dem Image füllt; `1`: `viper up` legt auch den fehlenden Cluster an; Zahl der Agent-Knoten dieses Clusters (0 bis 16) | leer, `0`, `0` |
| `K8S_DOCKER`, `K8S_DOCKER_HOST`, `K8S_DOCKER_IMAGE` | Docker auf dem Rechner: `dind` (privilegierter Sidecar), `remote` (Daemon unter `K8S_DOCKER_HOST`) oder `none`; Image des Sidecars (nur `kubernetes`) | `dind`, leer, `docker:dind` |
| `NATS_ENABLED` | `true`: der Rechner gehört zu einer NATS-Gruppe (nur Hetzner); `false` oder leer: aus | `false` |
| `NATS_GROUP` | Name der NATS-Gruppe; Pflichtangabe mit `NATS_ENABLED=true`; 1 bis 31 Kleinbuchstaben, Ziffern, Bindestriche | leer |
| `NATS_PORT` | Port des NATS-Servers im privaten Netzwerk (1024 bis 65535) | `4222` |
| `GROUP_MACHINES` | nur in `groups/<gruppe>.env`: die Rechner der Gruppe, durch Komma getrennt | – |
| `NATS_MCP` | `true`: `viper up` installiert auf dem Rechner den MCP-Server `viper-nats-mcp` und trägt ihn bei Claude Code ein (Benutzerbereich, Name `nats`); braucht `NATS_ENABLED=true` | `false` |
| `NATS_MCP_PUSH_FROM` | Maschinennamen, durch Komma getrennt (der eigene Name wird übergangen, sodass dieselbe Liste in einer Gruppendatei für alle Rechner gilt), deren Nachrichten der Server in eine Claude-Code-Sitzung schiebt (nur wenn Sie Claude selbst mit dem Channel-Schalter starten, siehe unten); leer: nichts wird geschoben | leer |
| `NATS_MCP_TOPICS` | Themen, durch Komma getrennt (höchstens 16, Namen wie bei `subscribe`), die der MCP-Server beim Start abonniert, wie mit dem Werkzeug `subscribe`, nur fest: `unsubscribe` lehnt sie ab. In einer Gruppendatei (`groups/<gruppe>.env`) gilt die Liste für alle Rechner der Gruppe; eine Änderung wirkt nach `viper up` und dem nächsten Start des Servers (neue Claude-Code-Sitzung) | leer |
| `NATS_OBJECTS` | `true`: Die Gruppe hat einen Speicher für Dateien (Bilder, PDFs, ZIP-Dateien), den der NATS-Server hält; braucht `NATS_MCP=true`. Steht in der **Gruppendatei** und gilt, wenn der Server der Gruppe angelegt wird; später ändern Sie es nur mit einem neuen Server (`viper down --group`, dann `viper up --group`) | `false` |
| `NATS_OBJECTS_MAX` | Gesamtgröße des Speichers in MiB (16 bis 102400); `viper up` prüft vorher, ob dieser Wert plus 1 GiB auf der Platte frei ist | `256` |
| `NATS_OBJECTS_FILE_MAX` | größte Datei in MiB (1 bis 64, höchstens `NATS_OBJECTS_MAX`) | `8` |
| `NATS_OBJECTS_TTL` | wie lange eine Datei bleibt, in Tagen (1 bis 365) | `7` |
| `NATS_MCP_AUTOALLOW` | Welche Werkzeuge des MCP-Servers Claude Code ohne Nachfrage nutzen darf: `off`, `read` (`peers`, `inbox`, `topics`) oder `all` (nur `all` erlaubt auch `fetch`, das eine Datei schreibt); ein früher gesetzter Eintrag wird bei jedem `viper up` ersetzt | `off` |
| `NATS_MCP_BINARY` | Datei, die statt des in `viper` eingebauten MCP-Servers installiert wird (nötig für ein mit `go build` gebautes `viper` ohne `make`) | leer |
| `DNS_PROVIDER` | Wer den DNS-Namen anlegt: `cloudflare` oder `none` (kein Eintrag, keine Domain, kein Cloudflare); wird nur aus den Einstellungsdateien gelesen | `cloudflare` |
| `DNS_DOMAIN` | Ihre Domain; Pflichtangabe mit Cloudflare | leer |
| `SSH_KEY` | Pfad Ihres Login-Schlüssels | `~/.ssh/hetzner-build` |
| `SSH_ALLOWED_CIDRS` | Netze, die Port 22 erreichen dürfen (gilt nicht für Pods und Container) | `0.0.0.0/0,::/0` |
| `IPAD_SSH_PUBLIC_KEY_FILE` | Öffentlicher Schlüssel des iPads | leer |
| `VIPER_HOMEBREW` | Homebrew (`brew`) auf dem Server installieren: `1` oder `0` | `1` |
| `VIPER_BREW_TAPS` | Homebrew-Taps (`owner/repo` oder `owner/repo=URL`), mit Leerzeichen getrennt | leer |
| `VIPER_BREW_TRUST` | Einträge für `brew trust`: `owner/repo` (Tap), `owner/repo/name` (Formel), `cask:…`, `command:…` | leer |
| `VIPER_BREW_PACKAGES` | Zu installierende Formeln (`name` oder `owner/repo/name`) | leer |
| `VIPER_BREWFILE` | `1`: eine `Brewfile` im geklonten Repository mit `brew bundle` anwenden | `0` |
| `VIPER_POST_CLONE` | Shell-Befehle, die nach dem Clone und nach Homebrew im geklonten Repository laufen (mehrere Zeilen: Wert in einfachen Anführungszeichen, dann kommen `$VARIABLEN` unverändert auf dem Server an; in doppelten würden sie auf Ihrem Computer aufgelöst). Brauchen ein Repository, laufen nur einmal direkt nach dem Clone (nach einem Fehler versucht es das nächste `viper up` erneut), ein Fehler ist standardmäßig nur eine Warnung (siehe `VIPER_POST_CLONE_ON_ERROR`). Log auf dem Server: `~/.config/viper/post-clone.log` | leer |
| `VIPER_POST_CLONE_ON_ERROR` | Bei einem Fehler der Befehle von `VIPER_POST_CLONE`: `retry` (Warnung, das nächste `viper up` versucht es erneut), `ignore` (Warnung, nie wieder) oder `abort` (`viper up` schlägt fehl, der Rechner bleibt, das nächste `viper up` versucht es erneut). Der Ausgang steht im Protokoll (`viper log`) | `retry` |
| `WORKSPACE_DIR` | Verzeichnis auf dem Server, in das die Repositories geklont werden (bei Containern nur unter `/home/builder/`) | `/home/builder/work` |
| `REPO_URL` | Repository, das beim Start geklont wird | leer |
| `REPO_BRANCH` | Zweig, der geklont wird | Standardzweig |
| `GIT_DEPLOY_KEY_FILE` | Deploy-Key für private SSH-Repositories | leer |
| `VIPER_GH_TOKEN` | GitHub-PAT für `gh` und `git` über HTTPS auf dem Server | leer |
| `VIPER_GH_HOST` | GitHub-Host (für GitHub Enterprise) | `github.com` |
| `VIPER_GIT_USER_NAME`, `VIPER_GIT_USER_EMAIL` | Autor für Commits auf dem Server (`git config --global`); was fehlt, kommt mit `VIPER_GH_TOKEN` vom GitHub-Konto | leer |
| `VIPER_SWAP` | Swap-Datei auf dem Server: `0` (aus), ein Faktor vom RAM zwischen 0,1 und 4 (`1` = so viel wie RAM, `0.5` = die Hälfte) oder eine Größe (`4G`, `512M`) | `0` |
| `VIPER_CODE_SERVER` | `1`: `code-server` (VS Code im Browser) installieren | `0` |
| `VIPER_CODE_SERVER_PASSWORD` | Passwort für `code-server`, mindestens 12 Zeichen ohne Leerzeichen; Pflicht mit `VIPER_CODE_SERVER=1` | leer |
| `VIPER_CLAUDE_TOKEN` | Langlebiges Token Ihres Claude-Abos aus `claude setup-token` (ein Jahr gültig) | leer |
| `VIPER_ANTHROPIC_BASE_URL` | Adresse einer Anthropic-kompatiblen Schnittstelle für ein anderes Modell (z. B. `https://ollama.com`) | leer |
| `VIPER_ANTHROPIC_AUTH_TOKEN` | API-Schlüssel für diese Schnittstelle | leer |
| `VIPER_CLAUDE_MODEL` | Modellname bei diesem Anbieter | leer |
| `VIPER_CLAUDE_MAX_CONTEXT_TOKENS` | Kontextfenster, das Claude Code für das Modell annimmt | 200.000 |
| `HCLOUD_TOKEN`, `CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_ZONE_ID` | Zugangsdaten (Shell oder `.env.local`), Pflichtangaben; `HCLOUD_TOKEN` nur bei Hetzner. Bei AWS kommen die Zugangsdaten aus der AWS-Anmeldung (siehe unten) | leer |

### AWS als Anbieter nutzen

Mit `PROVIDER=aws` in `.env.local` (oder in `machines/<rechner>.env.local`) legt `viper up` den Server als EC2-Instanz an. Die übrigen Befehle bleiben gleich.

* **Anmeldung:** `viper` nutzt die Anmeldung der AWS-CLI, also `aws login`, SSO (`aws sso login --profile <name>`) oder Zugangsschlüssel in der Umgebung. Ein Profil geben Sie mit `AWS_PROFILE` an. `viper preflight <rechner>` zeigt, ob die Anmeldung gilt.
* **Wo der Server liegt:** in der Default-VPC der Region, in einem Subnetz mit öffentlicher Adresse. Gibt es keine Default-VPC, setzen Sie `AWS_SUBNET_ID` oder `AWS_VPC_ID`. Mit einer Zone in `SERVER_LOCATION` (z. B. `eu-central-1b`) wählt `viper` ein Subnetz dieser Zone.
* **Was angelegt wird:** eine Instanz, ein Schlüsselpaar mit Ihrem Login-Schlüssel und eine Security Group `<SERVER_PREFIX>-ssh` (SSH und `ping` aus `SSH_ALLOWED_CIDRS`). Alles trägt das Tag `viper=<server>` (und `Name`). Die Instanz erzwingt IMDSv2.
* **Eigene Tags:** `AWS_TAGS=owner=me,project=x,ttl=2026-11-01T12:00:00Z` gibt Instanz und Systemplatte schon beim Anlegen (`RunInstances`) diese Tags mit, sodass auch Richtlinien mit `aws:RequestTag/...` den Start zulassen. `Name` und `viper` sind reserviert. Schlüsselpaar und Security Group sind gemeinsam genutzt, bleiben nach `viper down` bestehen und bekommen `AWS_TAGS` deshalb nicht, sondern `AWS_TAGS_SHARED` (gleiches Format und gleiche Grenzen), z. B. `project=x,managed-by=viper`, ohne `ttl`: Ein Aufräumer könnte sonst die Security Group löschen, die andere Rechner noch brauchen. Verlangt eine Richtlinie `aws:RequestTag/...` auch für `ImportKeyPair` und `CreateSecurityGroup`, setzen Sie die Pflicht-Tags hier. Ein schon vorhandenes Schlüsselpaar oder eine vorhandene Security Group bekommt die Tags nicht nachträglich; bei mehreren Rechnern entscheidet der Rechner, der sie zuerst anlegt. Grenzen von AWS: Schlüssel höchstens 128, Wert höchstens 256 Zeichen, nur Buchstaben, Ziffern, Leerzeichen und `_ . : / = + - @`, kein Präfix `aws:`, höchstens 50 Tags je Ressource (mit `Name` und `viper`); ein Wert kann kein Komma enthalten. `viper` prüft das beim Laden der Einstellungen, also auch vor `viper up`. Die Tags gelten nur beim Anlegen: Einen schon vorhandenen Server ändert `viper up` nicht. Tag-Werte stehen in der AWS-Konsole und in Kostenberichten: keine Geheimnisse eintragen.
* **Image:** `ubuntu-24.04` löst `viper` über den von Canonical veröffentlichten AMI-Parameter auf, passend zur Architektur des Instanztyps (`t3.*` x86, `t4g.*` ARM). Eine eigene AMI geben Sie als `ami-…` an.
* **Berechtigungen:** Die Rolle muss Instanzen starten, beschreiben und beenden, Security Groups anlegen und ergänzen, Schlüsselpaare importieren und den SSM-Parameter von Canonical lesen dürfen. Mit `AWS_TAGS` oder `AWS_TAGS_SHARED` braucht sie außerdem `ec2:CreateTags` (beim Anlegen; eine Bedingung `ec2:CreateAction` ist möglich).
* **Kosten:** AWS rechnet die Instanz und die Platte ab, solange der Server existiert. `viper down <rechner>` beendet die Instanz; Schlüsselpaar und Security Group bleiben für weitere Rechner erhalten.

### Kubernetes: Einstellungen und was `viper` im Cluster anlegt

Die Aufgabe beschreibt das Kapitel „Einen Rechner als Pod in einem Kubernetes-Cluster betreiben“. Hier steht das Nachschlagbare.

* **Einstellungen:** `PROVIDER=kubernetes`, `SERVER_LOCATION` (der Kontext der `kubeconfig`, Pflicht, ohne Standard: `viper` nimmt nie den „aktuellen“ Kontext, damit nichts im falschen Cluster entsteht), `K8S_IMAGE` (Standard: das veröffentlichte Image zu Ihrer Version von `viper`), `SERVER_TYPE` (`<CPUs>x<GiB>`, Standard `2x4`), `K8S_NAMESPACE` (Standard `viper`), `K8S_STORAGE_CLASS` (Standard: die des Clusters), `K8S_DISK_GB` (Heimverzeichnis, Standard 20), `K8S_BREW_GB` (Homebrew, Standard 10), `K8S_DOCKER` (`dind`, `remote` oder `none`; Standard `dind`), `K8S_DOCKER_HOST` (bei `remote`, etwa `tcp://host:2375`), `K8S_DOCKER_IMAGE` (Image des Daemons bei `dind`, Standard `docker:dind`), `K8S_IMAGE_PULL_SECRET` (Secret für ein Register mit Anmeldung, Standard: keines). `WORKSPACE_DIR` muss unter `/home/builder/` liegen (Standard `/home/builder/work`). Die Zeilen der Tabelle oben nennen sie noch einmal.
* **Abgelehnt** werden für Pods: ein `DNS_PROVIDER` außer `none`, `VIPER_SWAP`, `VIPER_CODE_SERVER=1`, ein `WORKSPACE_DIR` außerhalb von `/home/builder/` (auch aus der allgemeinen `.env.local`: setzen Sie es für den Rechner um) und ein unbekannter Wert von `K8S_DOCKER`. `SSH_ALLOWED_CIDRS` wird ignoriert.
* **Was anders ist als bei einer VM:** Der Pod läuft als Benutzer `builder` ohne root und ohne `sudo`; Docker-Client, Homebrew, Claude Code und die NATS-Programme sind im Image. Es gibt keine Firewall, keinen DNS-Namen und keine Adresse.
* **Was `viper` im Cluster anlegt:** je Rechner ein StatefulSet mit einem Pod, ein Secret (die Schlüssel) und zwei Volumes (`<name>-home`, `<name>-brew`); je NATS-Gruppe eine NetworkPolicy und einen Service `viper-nats-<gruppe>`. Alles trägt das Label `viper-managed`; `viper` ändert und löscht nur, was es trägt. Den Namespace legt `viper` an, falls er fehlt, und löscht ihn nie. Fehlt das StatefulSet eines Rechners, weil `viper down` unterbrochen wurde oder jemand es anders gelöscht hat (etwa mit `kubectl delete`), bleiben Secret und Volumes samt Daten stehen. `viper down <rechner>` nennt sie dann und löscht sie nach der Abfrage `CLEANUP`; `viper up <rechner>` nimmt die Volumes dagegen wieder in Betrieb (nur was ein unterbrochenes `viper down` schon als gelöscht markiert hatte, ersetzt es durch neue).
* **Docker:** Mit `dind` ist der Pod erst bereit, wenn der Daemon antwortet. Der Daemon lauscht nur im Pod selbst (ohne Anmeldung ist er sonst für jeden Pod des Clusters offen). Er hat sein eigenes Lager für Images, das mit dem Pod verschwindet, und keine eigenen Grenzen für CPU und Speicher. Der Preflight warnt vor dem privilegierten Container und meldet einen Fehler, wenn der Namespace Pod Security mit einer anderen Stufe als `privileged` erzwingt.
* **Register mit Anmeldung:** `K8S_IMAGE_PULL_SECRET` nennt ein Secret vom Typ `docker-registry`, das Sie selbst im Namespace anlegen. `viper` trägt es in den Pod ein und prüft im Preflight, dass es da ist; es ändert oder löscht es nie. Das Secret muss im Namespace liegen, den `K8S_NAMESPACE` nennt; legen Sie den Namespace dafür zuerst an.
* **Nicht geprüft** sind VS Code Remote-SSH über den Proxy, ein Register mit Anmeldung (`K8S_IMAGE_PULL_SECRET`) und EKS, GKE und AKS in einem echten Konto. Die Anzeige von Arbeitsspeicher und Platte (`viper check`, `viper ssh`) zeigt die Werte des Knotens, nicht die Grenzen des Pods. `viper find` kennt für Kubernetes keine Preise.

### Docker Compose: Einstellungen und was `viper` auf dem Docker-Host anlegt

Die Aufgabe beschreibt das Kapitel „Einen Rechner als Container auf dem eigenen Docker-Host betreiben“. Hier steht das Nachschlagbare.

* **Einstellungen:** `PROVIDER=compose`, `SERVER_LOCATION` (der Docker-Kontext, Pflicht, ohne Standard: `viper` nimmt nie den „aktuellen“ Kontext, damit nichts auf dem falschen Host entsteht; `default` ist der lokale), `COMPOSE_IMAGE` (Standard: das veröffentlichte Image zu Ihrer Version von `viper`), `SERVER_TYPE` (`<CPUs>x<GiB>`, Standard `2x4`), `COMPOSE_DOCKER` (`dind`, `remote` oder `none`), `COMPOSE_DOCKER_HOST` (bei `remote`), `COMPOSE_DOCKER_IMAGE` (Image des Daemons bei `dind`, Standard `docker:dind`). `WORKSPACE_DIR` muss unter `/home/builder/` liegen (Standard `/home/builder/work`). Eine Größe für die Volumes gibt es nicht.
* **Abgelehnt** werden: ein `DNS_PROVIDER` außer `none`, `VIPER_SWAP`, `VIPER_CODE_SERVER=1`, ein `WORKSPACE_DIR` außerhalb von `/home/builder/`, ein unbekannter Wert von `COMPOSE_DOCKER` und `remote` ohne `COMPOSE_DOCKER_HOST`. `SSH_ALLOWED_CIDRS` wird ignoriert.
* **Was `viper` anlegt:** Die Rechner einer **Gruppe** (`groups/<gruppe>.env`) liegen in **einem** Compose-Projekt `viper_g_<gruppe>` mit **einer** `compose.yaml` (ein Dienst je Rechner, `docker compose -p viper_g_<gruppe> ps` zeigt alle). Ein Rechner ohne Gruppe hat sein eigenes Projekt `viper-<präfix>-<rechner>`. In beiden Fällen gibt es je Rechner den Container des Rechners (Größe als `cpus` und `mem_limit`, Neustart `unless-stopped`), mit `dind` den Container `<name>-docker`, und zwei Volumes `<name>-home` und `<name>-brew`. Alles trägt das Label `viper-managed`; `viper` ändert und löscht nur, was es trägt. Die `compose.yaml` und die Schlüssel liegen unter `~/.local/state/viper/compose/g_<gruppe>/` (Schlüssel je Rechner in `secret/<rechner>/`) bzw. `~/.local/state/viper/compose/<name>/`. `viper` schreibt die Datei bei jedem `viper up` eines neuen Rechners neu; **was Sie von Hand hineinschreiben, geht verloren**, sie ist nicht zum Bearbeiten da. `viper up` und `viper down` einzelner Rechner einer Gruppe starten und entfernen nur deren Dienste, die anderen laufen weiter; mit dem letzten Rechner verschwinden Datei und Verzeichnis. Mehrere Rechner einer Gruppe starten gleichzeitig (die Datei wird nacheinander geändert). **Rechner, die eine ältere Version in einem eigenen Projekt angelegt hat, laufen so weiter**; erst ein `viper down` und `viper up` legt sie in der Datei der Gruppe an.
* **Reste:** Fehlt der Container eines Rechners, weil `viper down` unterbrochen wurde oder jemand ihn anders gelöscht hat (etwa mit `docker rm`), bleiben Volumes und Dateien samt Daten stehen. `viper down <rechner>` nennt sie dann und löscht sie nach der Abfrage `CLEANUP`; `viper up <rechner>` nimmt die Volumes dagegen wieder in Betrieb (nur was ein unterbrochenes `viper down` schon als gelöscht markiert hatte, ersetzt es durch neue).
* **Der Preflight** prüft Docker, den Kontext (nur ein lokaler Daemon), das Compose-Plugin, das Image und die Größe; er meldet `dind` ohne ausdrückliches `COMPOSE_DOCKER` als Fehler und warnt bei 20 oder mehr Docker-Netzen.
* **Nicht geprüft** sind Docker Desktop, rootless Docker und Podman als Host sowie VS Code Remote-SSH über den Proxy. Die Anzeige von Arbeitsspeicher und Platte (`viper check`) zeigt die Werte des Hosts, nicht die Grenzen des Containers. `viper find` kennt für Compose keine Preise.

### Google Cloud und Azure als Anbieter (nur gegen nachgebildete Schnittstellen getestet)

Mit `PROVIDER=gcp` bzw. `PROVIDER=azure` richtet `viper up` den Server bei Google Compute Engine bzw. Microsoft Azure ein. **Diese beiden Anbieter sind gebaut und gegen nachgebildete Schnittstellen getestet; ein Lauf in einem echten Konto ist nicht belegt (das Projekt hat dafür keinen Test, anders als bei AWS).** Prüfen Sie zuerst mit `viper preflight <rechner>` und einem kleinen Server, und rechnen Sie mit Fehlern. Hetzner und AWS sind dagegen in echten Konten getestet.

* **Google Cloud:** Anmeldung mit `gcloud auth application-default login` (oder einer Dienstkonto-Datei in `GOOGLE_APPLICATION_CREDENTIALS`). `GCP_PROJECT` ist Pflicht. Der Server ist eine VM in der Zone aus `SERVER_LOCATION` im Netz `default` (oder `GCP_NETWORK`) mit öffentlicher Adresse. Die Firewall besteht aus zwei VPC-Regeln `<SERVER_PREFIX>-ssh` (IPv4) und `<SERVER_PREFIX>-ssh-ipv6`, die für VMs mit dem Netzwerk-Tag `<SERVER_PREFIX>-ssh` gelten. Die Rolle braucht Rechte für Compute-Instanzen und Firewall-Regeln.
* **Azure:** Anmeldung mit `az login`. `AZURE_SUBSCRIPTION_ID` ist Pflicht. `viper` legt in der Ressourcengruppe (`viper`) ein virtuelles Netz `viper-vnet` mit Subnetz, eine Netzwerksicherheitsgruppe `<SERVER_PREFIX>-ssh` sowie je Server eine öffentliche Adresse, eine Netzwerkkarte und eine VM an. Alles trägt das Tag `viper`. `viper down` löscht VM, Netzwerkkarte, Adresse und Platte; Gruppe, Netz und Sicherheitsgruppe bleiben.
* **Schlüssel:** Beide Anbieter kennen keine Schlüsselobjekte; der Login-Schlüssel geht mit dem Server (cloud-init).

### Firewall und Anmeldung

* Die Firewall erlaubt eingehend SSH (TCP-Port 22) und ICMP (`ping`), beides aus den Netzen in `SSH_ALLOWED_CIDRS`. Fehlt eine der beiden Regeln, ergänzt `viper up` sie, auch in einer schon vorhandenen Firewall. Andere Regeln lässt es unverändert. Eine bestehende Regel ändert es nicht: Wollen Sie die Netze nachträglich einschränken, ändern Sie die Regeln in der Hetzner Console (bei AWS: an der Security Group in der EC2-Konsole).
* Die SSH-Konfiguration jedes Rechners verbietet Agent- und X11-Weiterleitung (`ForwardAgent no`, `ForwardX11 no`), auch wenn Ihre `~/.ssh/config` sie allgemein erlaubt. Der Agent auf dem Server hat Root-Rechte und soll Ihre lokalen SSH-Schlüssel nicht benutzen können.
* Standardmäßig ist SSH aus dem ganzen Internet erreichbar. Anmeldung ist nur mit SSH-Schlüssel möglich. Passwörter und Root-Login sind abgeschaltet. Schränken Sie die Netze ein, wenn Sie nur von festen Adressen arbeiten. Eine Alternative ist ein VPN.
* Ausgehende Verbindungen des Servers sind nicht eingeschränkt.

### DNS

Der DNS-Eintrag ist ein A-Record ohne Cloudflare-Proxy, weil dieser SSH nicht weiterleitet. `viper` ändert oder löscht nur Einträge, die es selbst angelegt hat (Kommentar `managed by viper`; Einträge mit dem Kommentar `managed by hetzner-claude` aus Versionen bis v0.21.1 gelten weiter als seine eigenen).

**Ohne Cloudflare:** Mit `DNS_PROVIDER=none` legt `viper` keinen Eintrag an. Sie brauchen weder `DNS_DOMAIN` noch Cloudflare-Token und -Zone. Den Rechner erreichen Sie mit `viper ssh <rechner>` oder über seine Adresse; `viper dns <rechner>` nennt die gespeicherte Adresse. Bei AWS zeigt `viper up` zusätzlich den Namen, den EC2 vergibt (`ec2-<adresse>.<region>.compute.amazonaws.com`). Er wechselt mit der Adresse. Andere Geräte (iPad) verbinden Sie dann über die Adresse; die Host-CA für `*.<DNS_DOMAIN>` entfällt. `viper check` prüft keinen DNS-Namen.

## 5. Rollen und Rechte

| Rolle | Wo | Rechte |
|---|---|---|
| Sie (lokal) | Ihr Rechner | Führen `viper` aus, besitzen API-Tokens, Login-Schlüssel und CA-Schlüssel. |
| `builder` | Server | Arbeitsbenutzer mit sudo ohne Passwort und Docker-Zugriff, entspricht Root. |
| `root` | Server | Anmeldung per SSH ist gesperrt. |

## 6. Import und Export

`viper` importiert und exportiert keine Daten. Ihre Arbeitsdateien liegen auf der Platte des Servers. Sichern Sie Ergebnisse durch `git push`.

## 7. Fehlerbehebung

### Meldung: "No .env.local: run viper setup to create it"

**Ursache:** Die Datei `.env.local` fehlt.  
**Lösung:** Führen Sie `viper setup` im Arbeitsverzeichnis aus (oder geben Sie es mit `--dir` an). Es legt `.env.local` aus der Vorlage an; tragen Sie dann `DNS_DOMAIN` ein.

### Meldung: "Missing settings: …"

**Ursache:** Pflichtwerte fehlen, je nach Anbieter und DNS-Modus: `DNS_DOMAIN`, `CLOUDFLARE_API_TOKEN` und `CLOUDFLARE_ZONE_ID` (mit Cloudflare), `HCLOUD_TOKEN` (Hetzner), `GCP_PROJECT` (Google Cloud) oder `AZURE_SUBSCRIPTION_ID` (Azure). Die Meldung nennt alle fehlenden auf einmal. Es wurde nichts angelegt oder gelöscht.  
**Lösung:** Tragen Sie die Werte in `.env.local` oder `machines/<rechner>.env.local` ein. Die Zugangsdaten (`HCLOUD_TOKEN`, `CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_ZONE_ID`) und alle `VIPER_*`-Einstellungen können Sie auch in der Shell exportieren; andere Einstellungen (etwa `DNS_DOMAIN`, `SERVER_TYPE`) stehen in den Dateien. Steht eine Einstellung in einer Datei, hat sie Vorrang vor der Shell. Beachten Sie: Eine leere Zeile `HCLOUD_TOKEN=` in `.env.local` überschreibt den Wert aus der Shell.

### Meldung: "DNS_DOMAIN must be a domain like example.com"

**Ursache:** `DNS_DOMAIN` enthält Großbuchstaben, ein Schema wie `https://`, einen Pfad oder einen Punkt am Ende.  
**Lösung:** Tragen Sie nur den Domainnamen ein, zum Beispiel `example.com`.

### Meldung: "Preflight: … problem(s); nothing was created."

**Ursache:** Die Vorabprüfung hat Probleme gefunden. Die Zeilen mit `FAIL` darüber nennen sie. Häufige Fälle:
* `… cannot currently be ordered in …` oder `… is not offered in …`: Der Servertyp ist am Standort ausverkauft oder dort nicht erhältlich. Die Meldung nennt die Standorte, an denen er bestellbar ist. Setzen Sie `SERVER_LOCATION` (etwa `nbg1` oder `hel1`) oder einen anderen `SERVER_TYPE`, am besten in `machines/<rechner>.env.local`.
* `server type … does not exist`, `location … does not exist`, `image … does not exist`: Tippfehler oder ein Wert, den der Anbieter nicht anbietet. Die gültigen Werte zeigt die Konsole des Anbieters (Typen, Standorte bzw. Regionen und Images).
* `DNS_DOMAIN … is not in Cloudflare zone …`: `CLOUDFLARE_ZONE_ID` gehört zu einer anderen Domain.
* `repository … not reachable without credentials`: Das Repository ist privat. Setzen Sie `VIPER_GH_TOKEN` (GitHub) oder nutzen Sie eine SSH-URL mit `GIT_DEPLOY_KEY_FILE`.
* `token cannot access …`: Das fein granulare Token gilt nicht für dieses Repository oder gehört zu einem anderen Resource owner. `token cannot push to …`: Das Token darf nur lesen; setzen Sie **Contents** auf „Read and write“ (Abschnitt „GitHub-CLI und Token auf dem Rechner nutzen“). Die Rechte eines vorhandenen Tokens können Sie bei GitHub nachträglich ändern (**Edit**); ein neues Token ist nicht nötig.

**Lösung:** Beheben Sie die genannten Punkte und führen Sie `viper preflight <rechner>` erneut aus.

### Meldung: "Machine name required" oder "Machine name: lowercase letters, digits, hyphens"

**Ursache:** Der Rechnername fehlt oder enthält ungültige Zeichen.  
**Lösung:** Geben Sie einen Namen aus Kleinbuchstaben, Ziffern und Bindestrichen an, zum Beispiel `viper up web`.

### Meldung: "Run viper setup first"

**Ursache:** Login-Schlüssel oder Host-CA fehlen.  
**Lösung:** Führen Sie `viper setup` aus.

### Meldung: "Hetzner: listing the … failed (check HCLOUD_TOKEN / project / network)" (bei AWS "AWS: …", bei Google Cloud "Google Cloud: …", bei Azure "Azure: …")

**Ursache:** Die API des Anbieters hat nicht geantwortet oder die Anmeldung abgelehnt: Das Token ist abgelaufen, gehört zu einem anderen Projekt oder hat keine Schreibrechte, die Anmeldung (`aws login`, `az login`, `gcloud auth application-default login`) ist abgelaufen, oder das Netz ist gestört. `viper` bricht dann ab, ohne etwas anzulegen, zu löschen oder aufzuräumen.  
**Lösung:** Prüfen Sie Token bzw. Anmeldung und Projekt, Konto oder Region (die Meldung nennt, was zu prüfen ist) und führen Sie den Befehl erneut aus.

### Meldung: "the pod … cannot start: … ImagePullBackOff" oder "… Unschedulable" oder "timed out waiting for the pod …" (Kubernetes)

**Ursache:** `ImagePullBackOff` oder `ErrImagePull`: Der Cluster findet das Image `K8S_IMAGE` nicht (bei k3d fehlt `--k3d` beim Bauen, bei anderen Clustern das `docker push`, oder das Register verlangt eine Anmeldung: dann `K8S_IMAGE_PULL_SECRET`). `Unschedulable`: Kein Knoten hat genug Platz für `SERVER_TYPE` oder die Volumes. Andere Fehler (ein Container, der abstürzt) zeigt `viper` erst nach zehn Minuten als „timed out“ an.
**Lösung:** Der Rechner bleibt stehen. Sehen Sie den Pod an: `kubectl -n viper describe pod <präfix>-<rechner>-0` und bei Docker `kubectl -n viper logs <präfix>-<rechner>-0 -c dockerd`. Beheben Sie die Ursache (Image in den Cluster bringen, `SERVER_TYPE` verkleinern) und führen Sie `viper up <rechner>` erneut aus; oder löschen Sie den Rechner mit `viper down <rechner> --force`.

### Meldung: "the login may not, in namespace …" oder "the cluster has no default storage class" (Kubernetes, `viper preflight`)

**Ursache:** Ihr Zugang darf im Namespace nicht alles, was `viper` tut (die Meldung nennt die fehlenden Rechte), oder der Cluster hat keine Standard-StorageClass für die Volumes.
**Lösung:** Lassen Sie die Rechte erweitern oder nennen Sie einen Namespace, in dem Sie sie haben (`K8S_NAMESPACE`); setzen Sie `K8S_STORAGE_CLASS` auf eine vorhandene Klasse.

### Reste im Cluster nach einer abgebrochenen Löschung (Kubernetes)

**Ursache:** `viper down` wurde unterbrochen oder ein Cluster war nicht erreichbar.
**Lösung:** `viper down <rechner>` meldet „Server … not found“, nennt Secret und Volumes, die übrig sind, und löscht sie nach der Abfrage `CLEANUP` (ihre Daten gehen verloren). Wollen Sie den Rechner dagegen mit seinen Daten wiederhaben, führen Sie `viper up <rechner>` aus: Es nimmt die Volumes wieder in Betrieb. Was `viper` angelegt hat, finden Sie mit `kubectl -n viper get all,pvc,secret,netpol -l viper-managed=true`.

### Reste auf dem Docker-Host nach einer abgebrochenen Löschung (Docker Compose)

**Ursache:** `viper down` wurde unterbrochen oder ein Container wurde von Hand gelöscht.
**Lösung:** `viper down <rechner>` meldet „Server … not found“, nennt Volumes, Container und Dateien, die übrig sind, und löscht sie nach der Abfrage `CLEANUP` (ihre Daten gehen verloren). Wollen Sie den Rechner dagegen mit seinen Daten wiederhaben, führen Sie `viper up <rechner>` aus. Was `viper` angelegt hat, finden Sie mit `docker ps -a --filter label=viper-managed=true` und `docker volume ls --filter label=viper-managed=true`.

### Meldung: "docker not running" nach einem Neustart des Containers (Docker Compose)

**Ursache:** Der Container des Rechners wurde allein neu gestartet (`docker restart`, Absturz, Neustart des Hosts); der zweite Container mit dem Docker-Daemon hängt dann am alten Netz.
**Lösung:** `viper up <rechner>` erkennt das, startet den zweiten Container neu und wartet, bis der Daemon antwortet.

### Meldung: "No UTF-8 locale detected!" (btop, tmux) in einem Pod oder Container

**Ursache:** Das Image der Maschine, mit der der Pod oder Container angelegt wurde, hat in den SSH-Sitzungen keine UTF-8-Sprachumgebung gesetzt (ältere Versionen). Das Image kennt nur `C.UTF-8`; schickt Ihr Terminal eine andere (zum Beispiel `de_DE.UTF-8`), die es dort nicht gibt, fallen Programme auf ASCII zurück.
**Lösung:** Sofort: `export LANG=C.UTF-8 LC_ALL=C.UTF-8` in der Sitzung (oder `btop --force-utf`). Dauerhaft: Ein Image ab dieser Korrektur setzt `C.UTF-8` für alle Sitzungen fest. Ein vorhandener Pod oder Container behält sein altes Image: Legen Sie den Rechner mit `viper down` und `viper up` neu an (Daten gehen dabei verloren, sichern Sie vorher), nachdem Sie auf eine neuere Version von `viper` oder ein neu gebautes Image (`viper image build`) umgestellt haben. Auf einer VM tritt das nicht auf.

### Meldung: "SSH not reachable; server retained."

**Ursache:** Der Server ist nach etwa 20 Minuten nicht per SSH erreichbar. Mögliche Gründe: Die Firewall blockiert Ihr Netz, oder der Server startet nicht. (Ein nicht vertrauenswürdiger Host-Schlüssel oder ein abgelehnter Login-Schlüssel hat eigene Meldungen, siehe unten.)  
**Lösung:**
1. Prüfen Sie die Firewall-Regel für Port 22 in der Konsole Ihres Anbieters (bei AWS die Security Group).
2. Öffnen Sie die Web-Konsole des Servers und lesen Sie `/var/log/cloud-init-output.log`.
3. Starten Sie `viper up <rechner>` erneut, wenn die Ursache behoben ist.
4. Hilft das nicht, löschen Sie den Rechner mit `viper down <rechner> --force` und legen Sie ihn neu an.

### Meldung: "SSH: the server rejects the login key"

**Ursache:** Der Server lehnt Ihren Login-Schlüssel ab (`Permission denied`). Meist wurde der Server mit einem anderen Login-Schlüssel angelegt, etwa von einem anderen Computer oder mit einem anderen `SSH_KEY`. Bei einem bereits laufenden Server meldet `viper up` das sofort, statt lange zu warten.  
**Lösung:** Nutzen Sie den Computer bzw. Schlüssel, mit dem der Rechner angelegt wurde, oder tragen Sie Ihren öffentlichen Schlüssel dort in `/home/builder/.ssh/authorized_keys` ein.

### Meldung: "Claude Code installation failed; server retained."

**Ursache:** Die Installation von Claude Code ist bei der Einrichtung und beim Nachholen gescheitert, meist wegen eines Netzwerkproblems. Steht davor `curl: (22) … error: 429`, drosselt der Installer die Adresse des Servers (Hetzner vergibt freigegebene Adressen schnell neu, so bekommen neue Server oft die Adresse eines früheren Testservers).  
**Lösung:** Neue Server und `viper up` versuchen die Installation selbst bis zu fünfmal mit wachsender Pause. Scheitert auch das, warten Sie einige Minuten und führen `viper up <rechner>` erneut aus; das richtet nur nach, was fehlt (auch Homebrew und die Pakete, die der Abbruch übersprungen hat). Die Antwort des Servers zeigt `viper ssh <rechner> --cmd "curl -sI https://claude.ai/install.sh"`, eine Zeile `retry-after` nennt die Wartezeit. Hilft Warten nicht, löschen Sie den Server und legen ihn später neu an, dann hat er vielleicht eine andere Adresse.

### Meldung: "Docker is not running"

**Ursache:** Die Servereinrichtung ist unvollständig, Docker läuft nicht.  
**Lösung:** Lesen Sie `/var/log/cloud-init-output.log` auf dem Server. Meist hilft es, den Rechner mit `viper down <rechner>` zu löschen und neu anzulegen.

### Meldung: "WARNING: cloud-init reported errors"

**Ursache:** cloud-init meldet Fehler, aber Claude Code und Docker laufen. Der Rechner ist nutzbar.  
**Lösung:** Prüfen Sie bei Bedarf `/var/log/cloud-init-output.log` auf dem Server.

### Meldung: "SSH key … belongs to a different public key"

**Ursache:** Beim Anbieter (Hetzner-Projekt, AWS-Region) gibt es schon einen SSH-Schlüssel namens `<SERVER_PREFIX>-login`, aber mit einem anderen Inhalt als Ihr Login-Schlüssel. Liegt Ihr Login-Schlüssel dagegen schon unter einem anderen Namen im Projekt, verwendet `viper up` ihn einfach.  
**Lösung:** Löschen oder benennen Sie den alten Schlüssel in der Konsole des Anbieters um, oder setzen Sie ein anderes `SERVER_PREFIX`.

### Meldung: "File not readable: …"

**Ursache:** Die Datei aus `GIT_DEPLOY_KEY_FILE` oder `IPAD_SSH_PUBLIC_KEY_FILE` fehlt oder ist nicht lesbar.  
**Lösung:** Prüfen Sie den Pfad und die Rechte der Datei. Es wurde noch nichts angelegt.

### Meldung: "error validating token" beim Start

**Ursache:** `gh` hat das Token aus `VIPER_GH_TOKEN` abgelehnt: Es ist abgelaufen, widerrufen oder falsch kopiert. Ein klassisches Token (nicht fein granular) braucht außerdem mindestens die Scopes `repo` und `read:org`. Der Server ist schon angelegt.  
**Lösung:** Korrigieren Sie `VIPER_GH_TOKEN` und führen Sie `viper up <rechner>` erneut aus. Der vorhandene Server wird weiterverwendet.

### Meldung: "Git check on the server failed. NOT deleting server"

**Ursache:** `viper down` konnte die Git-Prüfung auf dem Server nicht ausführen, etwa wegen eines Verbindungsabbruchs.  
**Lösung:** Führen Sie `viper down <rechner>` erneut aus. Ist der Server dauerhaft nicht erreichbar, nutzen Sie `viper down <rechner> --force`.

### Meldung: "Existing DNS record is not managed by this setup; refusing overwrite"

**Ursache:** Unter diesem Namen existiert ein DNS-Eintrag, den `viper` nicht angelegt hat.  
**Lösung:** Wählen Sie einen anderen Rechnernamen oder löschen Sie den fremden Eintrag in Cloudflare, falls er nicht mehr gebraucht wird.

### Meldung: "Multiple A records; refusing to modify"

**Ursache:** Es gibt mehrere A-Records mit diesem Namen.  
**Lösung:** Löschen Sie in Cloudflare alle überflüssigen Einträge.

### Meldung: "SSH unreachable. Use viper down <machine> --force …"

**Ursache:** `viper down` erreicht den Server nicht (Netz, Firewall oder der Server läuft nicht).  
**Lösung:** Prüfen Sie Netz und Firewall. Ist der Server nicht zu retten, nutzen Sie `viper down <rechner> --force`.

### Meldung: "host key is not trusted" (bei `viper up` oder `viper down`)

**Ursache:** Der Server antwortet, beweist aber nicht, dass er der erwartete Rechner ist. Mögliche Gründe:
* Der Rechner wurde von einem anderen Computer aus gelöscht und neu angelegt, und dieser Computer hat noch den Schlüssel des alten Servers hinterlegt. Das erkennt `viper up` normalerweise an der geänderten Server-ID und verwirft den alten Schlüssel selbst.
* Auf diesem Computer ist kein Schlüssel hinterlegt, und das Zertifikat ist abgelaufen.
* Jemand sitzt zwischen Ihnen und dem Server.

**Lösung:** Nutzen Sie **nicht** einfach `--force`, denn dann prüft `viper down` keine ungesicherte Arbeit. Arbeiten Sie vom Computer aus, der den Rechner angelegt hat, oder prüfen Sie den Fingerabdruck in der Web-Konsole des Servers (`ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub`). Erst wenn Sie sicher sind, dass auf dem Server nichts Wichtiges liegt, löschen Sie ihn mit `viper down <rechner> --force`.

### Meldung: "WARNING: DNS removal failed"

**Ursache:** Der DNS-Eintrag konnte nicht gelöscht werden, der Server ist aber gelöscht.  
**Lösung:** Entfernen Sie den A-Record in Cloudflare von Hand.

### Meldung: "The NATS group … has a network but no server"

Der erste Start einer NATS-Gruppe wurde unterbrochen: Das private Netzwerk `viper-nats-<gruppe>` besteht seit mehr als 15 Minuten, aber es gibt keinen Server. Starten Sie `viper up` noch einmal auf dem Rechner, der der Server werden sollte. Gibt es ihn nicht mehr, löschen Sie die übrigen Rechner der Gruppe mit `viper down` und das Netzwerk in der Hetzner Console.

### Meldung: "The server … belongs to the NATS group …, not … (NATS_GROUP)"

Ein vorhandener Rechner ist in einer anderen NATS-Gruppe, als `NATS_GROUP` jetzt sagt. `viper` verschiebt ihn nicht. Setzen Sie `NATS_GROUP` zurück oder löschen Sie den Rechner zuerst.

### Meldung: "… is the NATS server of the group …, and the group has other machines"

`viper down` löscht einen NATS-Server nicht, solange die Gruppe weitere Rechner hat. Löschen Sie zuerst die anderen (`viper down <rechner>`) oder beenden Sie die Gruppe mit `viper down <server> --force`.

### Meldung: "Revoking the key of … failed … The machine was NOT deleted"

Beim Löschen eines Clients konnte sein Schlüssel nicht beim NATS-Server entfernt werden (Server nicht erreichbar). Prüfen Sie den Server mit `viper check <server>` oder löschen Sie den Client mit `viper down <rechner> --force`.

### Meldung: "The machine … is named in the groups …: say which one applies with --in-group"

Der Rechner steht in `GROUP_MACHINES` von mehreren Gruppendateien, und `viper` weiß nicht, welche gelten soll. Sagen Sie es mit `--in-group`, zum Beispiel `viper up web --in-group dev` (die Meldung nennt die Gruppen). Läuft der Rechner schon, gilt die Gruppe, in der er gestartet wurde, und die Meldung erübrigt sich. `--group dev` meint dagegen alle Rechner der Gruppe. Soll der Rechner nur in einer Gruppe stehen, entfernen Sie ihn aus `GROUP_MACHINES` der anderen Dateien.

### Meldung: "No group …" oder "groups/….env: GROUP_MACHINES: …"

`--group` nennt eine Gruppe ohne Datei `groups/<gruppe>.env`, oder die Datei ist fehlerhaft (kein Name, ein Name doppelt, falsche Schreibweise, `NATS_GROUP` abweichend vom Dateinamen). Die Meldung nennt die Zeile. Namen bestehen aus Kleinbuchstaben, Ziffern und Bindestrichen; mehrere Namen mit Leerzeichen setzen Sie in Anführungszeichen.

### Meldung: "Nothing was made: the checks of the group failed."

`viper up --group` prüft zuerst alle Rechner (`viper preflight`) und legt nichts an, wenn eine Prüfung scheitert. Die Zeilen davor nennen den Rechner (`[beta] FAIL …`) und das Problem.

### Meldung: "skipped: … failed before its server existed" oder "stopped because the server … failed"

Beim Start einer neuen NATS-Gruppe scheiterte der erste Rechner mit NATS, der den Server bekommt. Davor starten keine Clients (`skipped`), danach werden die laufenden Starts abgebrochen (`stopped`). Beheben Sie die Ursache beim ersten Rechner (seine Zeilen stehen davor) und wiederholen Sie `viper up --group`; vorhandene Server werden wiederverwendet.

### Meldung: "cannot tell its role in the NATS group; nothing was deleted" (`down --group`)

`viper` konnte beim Anbieter nicht lesen, welcher Rechner der NATS-Server ist (oft eine Anbieter-Störung oder ein falsches Token). Es löscht dann nichts, denn der Server muss zuletzt gehen. Prüfen Sie mit `viper check --group` und wiederholen Sie den Befehl.

### Meldung: "the MCP server on the machine does not run" oder "… is not the one of this viper" (`viper check`)

Die Binary von `viper-nats-mcp` auf dem Rechner läuft nicht (falsche Architektur bei `NATS_MCP_BINARY`) oder stammt aus einer anderen Version als Ihr `viper`. Wiederholen Sie `viper up <rechner>`; es ersetzt die Binary erst, nachdem die neue auf dem Rechner läuft.

### Meldung: "did not say that it understands attachments" oder "… has no object store" (Dateien senden)

Zu „did not say …“: Die Maschine hat nicht gemeldet, dass sie Dateien versteht (eine ältere Version von `viper-nats-mcp`, noch keine Meldung, oder ihre Gruppe hat keinen Speicher für Dateien); sie würde die ganze Nachricht ablehnen. `viper up <rechner>` installiert die passende Version, und `peers` zeigt die Versionen. „… has no object store“ (die Meldung nennt die Gruppe): Setzen Sie `NATS_OBJECTS=true` in der Gruppendatei und legen Sie den Server neu an (`viper down --group G`, `viper up --group G`); `viper check` nennt es ebenfalls.

### Meldung: "NATS_OBJECTS is on, but the server of the group … was made without an object store" (und umgekehrt)

Der Objektspeicher wird beim Anlegen des Servers festgelegt, und `viper up` für den Server-Rechner bricht ab, wenn die Einstellung nicht zu ihm passt, bevor etwas geändert wird. Haben Sie `NATS_OBJECTS` später ein- oder ausgeschaltet, setzen Sie es wieder wie beim Anlegen, oder legen Sie die Gruppe neu an (`viper down --group G`, dann `viper up --group G`; das löscht alle Rechner der Gruppe).

### Meldung: "the object store of the group is full" oder "the file is not in the object store" (Dateien)

Der Speicher ist voll (`NATS_OBJECTS_MAX`): Dateien gehen, wenn ihre Zeit um ist (`NATS_OBJECTS_TTL`), vorher hilft nur ein größerer Speicher mit einem neuen Server. „Not in the object store“: Die Datei ist abgelaufen, wurde von einer Maschine unauffindbar gemacht, oder der Server wurde neu angelegt. „Does not match the checksum“ oder „damaged“: Die Datei wurde verändert oder ist nicht die der Nachricht; sie wird nicht gespeichert.

### Meldung: "the server certificate has expired" (NATS)

Das Zertifikat des NATS-Servers gilt einen Monat und wird nicht erneuert. Löschen Sie die Rechner der Gruppe und legen Sie sie neu an.

### `ping <rechner>.<domain>`: "Name oder Dienst nicht bekannt"

**Ursache:** Ihr DNS-Server merkt sich, dass es den Namen nicht gibt. Das passiert, wenn der Name abgefragt wurde, während der DNS-Eintrag nicht existierte, etwa zwischen `viper down` und `viper up`. Die Zone erlaubt das Zwischenspeichern dieser negativen Antwort für bis zu 30 Minuten (`viper check` nennt den genauen Wert). Oft betrifft das den DNS-Server eines VPNs, während öffentliche Resolver den Namen schon auflösen.  
**Lösung:** Prüfen Sie mit `viper check <rechner>`, ob Cloudflare 1.1.1.1 und Google 8.8.8.8 den Namen richtig auflösen. Dann liegt es nur an Ihrem DNS-Server: warten Sie bis zu 30 Minuten, oder nutzen Sie vorübergehend einen anderen DNS-Server (zum Beispiel ohne VPN). `resolvectl flush-caches` leert nur den Cache Ihres Computers, nicht den des DNS-Servers.

### `ping` bekommt keine Antwort

**Ursache:** Die Firewall des Rechners lässt kein ICMP durch. Ältere Firewalls hatten nur die SSH-Regel.  
**Lösung:** Führen Sie `viper up <rechner>` aus. Das ergänzt die ICMP-Regel in der Firewall, der Server bleibt dabei bestehen. Ob der Rechner erreichbar ist, zeigt auch ohne Ping `viper check <rechner>`.

### Meldung: "WARNING: some Homebrew taps, trust entries or formulae failed"

**Ursache:** Ein Tap, ein Trust-Eintrag, eine Formel oder die `Brewfile` ließ sich nicht einrichten, etwa wegen eines Tippfehlers im Namen, eines nicht erreichbaren Taps oder eines Build-Fehlers. Die Zeilen mit `FAILED` darüber nennen den Schritt.  
**Lösung:** Lesen Sie das Protokoll mit `viper ssh <rechner> --cmd "tail -n 50 ~/.config/viper/brew-packages.log"`, korrigieren Sie die Einstellung und führen Sie `viper up <rechner>` erneut aus.

### Verbindung wird nach einigen Tagen abgelehnt

**Ursache:** Das Host-Zertifikat ist nach sieben Tagen abgelaufen. Eine automatische Erneuerung gibt es nicht. Computer mit hinterlegtem Host-Schlüssel verbinden sich weiter. Das ist der Computer, der den Rechner angelegt hat, und jeder andere, der innerhalb der sieben Tage einmal `viper up` oder `viper down` ausgeführt hat (dabei wird der Schlüssel über die noch gültige CA-Prüfung nachgetragen). Betroffen sind Geräte, die nur der Host-CA vertrauen, etwa das iPad, und Computer, die den Rechner nie innerhalb der Gültigkeit erreicht haben.  
**Lösung:** Arbeiten Sie von einem Computer mit hinterlegtem Schlüssel aus, pushen Sie dort Ihre Arbeit, beenden Sie den Rechner mit `viper down <rechner>` und starten Sie ihn neu.

### Meldung: "viper ui: cannot listen on 127.0.0.1:…", die Seite zeigt „Nicht erlaubt“ oder „Die Sitzung ist zu Ende“

**Ursache:** Der Port ist belegt, die Adresse wurde nicht so geöffnet, wie `viper ui` sie ausgibt, oder `viper ui` wurde inzwischen neu gestartet.  
**Lösung:**

* **„cannot listen … address already in use“:** Wählen Sie mit `viper ui --port N` einen freien Port, oder lassen Sie `--port` weg, dann nimmt `viper` einen freien.
* **„Nicht erlaubt“ (403):** Öffnen Sie die Adresse genau so, wie `viper ui` sie im Terminal ausgegeben hat: mit `127.0.0.1` (nicht `localhost`), mit dem Schlüssel, der nur einmal und nur kurz nach dem Start gilt (eine Minute, mit `--no-open` zehn). Danach öffnen Sie die Seite im selben Browser ohne Schlüssel (`http://127.0.0.1:<port>/`).
* **„Die Sitzung ist zu Ende“:** `viper ui` wurde neu gestartet. Starten Sie es neu und öffnen Sie die neue Adresse.
* **Der Browser öffnet sich nicht** (`xdg-open`, unter macOS `open`, unter Windows `rundll32`): Öffnen Sie die Adresse mit dem Schlüssel, die `viper ui` ausgibt, von Hand, oder starten Sie mit `--no-open`.
* **Eine Datei lässt sich nicht senden:** Die Gruppe braucht `NATS_OBJECTS=true`; die Datei darf nicht größer sein als `NATS_OBJECTS_FILE_MAX` der Gruppe (in der Oberfläche höchstens 64 MiB). Mit `--readonly` geht nichts.

## 8. FAQ

**Kann ich einen Rechner nur pausieren?**  
Nein. `viper down` löscht den Server samt Platte. `viper up` richtet ihn neu ein und klont das Repository erneut.

**Was kostet es, wenn nichts läuft?**  
Nichts mehr. Ein gelöschter Server wird nicht berechnet. Prüfen Sie in der Konsole Ihres Anbieters, dass wirklich nichts übrig ist. Bei AWS, Google Cloud und Azure bleiben Schlüsselpaar, Firewall bzw. Netz für weitere Rechner bestehen (sie kosten nichts).

**Muss ich mich bei Claude nach jedem neuen Server neu anmelden?**  
Nur ohne `VIPER_CLAUDE_TOKEN`: Die Anmeldung mit `/login` liegt im Home-Verzeichnis des Servers und wird mit ihm gelöscht. Mit dem Token ist jeder neue Server sofort angemeldet (siehe „Claude Code ohne erneute Anmeldung nutzen“).

**Kann ich einen Rechner mit anderem Servertyp starten?**  
Ja. Setzen Sie `SERVER_TYPE` in `machines/<rechner>.env.local`. Bei einem bereits laufenden Server wirkt die Änderung erst nach Löschen und Neustart.

**Funktionieren bestehende Server und Volumes aus der Vorversion?**  
Nein. Die Namen haben sich geändert (seit Handbuch 1.8 auch DNS-Namen und lokale Dateinamen, jetzt mit Präfix), und es gibt keine separaten Volumes mehr. Löschen Sie alte Server mit der Version von `viper`, mit der Sie sie angelegt haben, und alte Volumes in der Hetzner Console, bevor Sie die neue Version verwenden.

**Darf ich die Tokens in die README oder in Git schreiben?**  
Nein. Verwenden Sie die Shell oder `.env.local`, die nicht eingecheckt wird.

**Kann ich die Oberfläche von einem anderen Rechner aus öffnen?**  
Nein. `viper ui` lauscht nur auf `127.0.0.1` und ist nur von dem Computer aus erreichbar, auf dem es läuft. Wer von außen zugreifen will, tunnelt selbst, auf eigene Verantwortung: Die Oberfläche kann Server anlegen und löschen.

## 9. Glossar

| Begriff | Bedeutung |
|---|---|
| Lauf | Ein Aufruf von `viper` aus der Oberfläche (`viper ui`) mit seiner Ausgabe, seinem Zustand und seinem Exit-Status |
| Zustand | In der Übersicht von `viper ui`: was dieser Computer über einen Rechner weiß (gestartet, angelegt, nicht gestartet); ob er wirklich läuft, sagt erst `viper check` |
| Rechner | Ein Server mit Namen, in der Regel für ein Repository |
| Arbeitsverzeichnis | Verzeichnis auf dem Server, in dem die Repositories liegen (`WORKSPACE_DIR`) |
| Host-CA | Lokale Zertifizierungsstelle, mit der der Server-Schlüssel signiert wird |
| Host-Zertifikat | Beglaubigung des Server-Schlüssels, sieben Tage gültig |
| Hinterlegter Host-Schlüssel | Öffentlicher Schlüssel des Servers, den `viper up` beim Anlegen speichert; Rückfall nach Ablauf des Zertifikats |
| PAT | Personal Access Token: Zugangsschlüssel für die GitHub-API anstelle eines Passworts |
| Tap | Zusätzliche Paketquelle für Homebrew (`owner/repo`) |
| Brewfile | Datei im Repository, in der Homebrew-Taps, Trust und Formeln stehen; `brew bundle` richtet sie ein |
| Deploy-Key | SSH-Schlüssel, der nur für ein Repository gilt |
| cloud-init | Automatische Ersteinrichtung des Servers beim ersten Start |
| tmux | Programm, das Terminal-Sitzungen dauerhaft hält |
| A-Record | DNS-Eintrag, der einen Namen auf eine IPv4-Adresse abbildet |
| Zone-ID | Kennung Ihrer Domain bei Cloudflare |
| NATS | Schlankes Nachrichtensystem; Programme senden und empfangen Nachrichten zu Themen (Subjects) |
| NATS-Gruppe | Rechner, die über einen gemeinsamen NATS-Server in einem privaten Netzwerk Nachrichten austauschen (`NATS_GROUP`) |
| NKey | Schlüsselpaar, mit dem sich ein Rechner am NATS-Server anmeldet; der geheime Teil (Seed) bleibt auf dem Rechner |

## 10. Support und Kontakt

Zu `viper` gibt es keinen eigenen Support. Prüfen Sie bei Problemen zuerst Kapitel 7. Für Ihren Cloud-Anbieter und Cloudflare gelten deren Dokumentation und Support. Beachten Sie: Echt gelaufen sind Einrichtung, Wiederverwenden, Neuanlegen, Check und Löschen auf Hetzner Cloud mit Cloudflare-DNS sowie Claude über das Abo-Token und Ollamas Cloud, aber mit den früheren Bash-Skripten, auf denen `viper` aufbaut. Das Go-Programm `viper` ist auf **AWS** in echten Läufen getestet: Vorabprüfung, Einrichtung mit `cloud-init` und SSH und Löschen, auf x86 und ARM, ohne Cloudflare (`DNS_PROVIDER=none`). Auf Hetzner, mit Cloudflare, bei Google Cloud und bei Azure ist es nur gegen nachgebildete Schnittstellen getestet, nicht gegen die echten APIs. Noch nicht echt gelaufen sind außerdem `VIPER_GH_TOKEN`/gh, Deploy-Keys, das iPad und ein vLLM-Server. Probieren Sie Neues in einem Testprojekt und mit `viper preflight` aus.

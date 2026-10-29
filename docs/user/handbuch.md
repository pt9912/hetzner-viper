# Benutzerhandbuch: Hetzner Viper

Version: 1.68  
Software-Version: v0.12.0  
Stand: 29.10.2026  

## 1. Einleitung

### Zweck der Software

Das Programm `viper` richtet mit einem Befehl einen entfernten Arbeitsrechner bei einem Cloud-Anbieter ein: Hetzner Cloud, AWS (Amazon EC2), Google Cloud oder Azure. Hetzner und AWS sind erprobt, Google Cloud und Azure noch nicht (Kapitel 4). Auf diesem Rechner arbeitet der Code-Agent Claude Code als „virtuelle Person“ (VIPER: VIrtual PERson) in Ihrem Repository. Sie arbeiten darauf über VS Code (Remote-SSH) oder über ein SSH-Terminal, zum Beispiel vom iPad.

Pro Git-Repository richten Sie einen eigenen Rechner ein. Beim Einrichten wird das Repository automatisch geklont. Wenn Sie fertig sind, löschen Sie den Rechner wieder. Alle Dateien auf dem Rechner werden dabei gelöscht. Sichern Sie Ihre Arbeit deshalb vorher mit `git push`.

### Zielgruppe

Dieses Handbuch richtet sich an Entwicklerinnen und Entwickler, die auf einem Linux-Rechner arbeiten und die Kommandozeile bedienen können.

### Voraussetzungen

Auf Ihrem lokalen Rechner (Linux oder macOS):

* Das Programm `viper` (eine einzelne Datei aus dem Release, siehe „`viper` installieren“).
* Der OpenSSH-Client mit `ssh`, `ssh-keygen`, `ssh-keyscan` und `scp`. Mehr brauchen Sie nicht: `hcloud`, `jq`, `curl`, Docker und `make` sind nicht nötig.
* Optional VS Code mit der Erweiterung **Remote-SSH**. Sie nutzt den OpenSSH-Client Ihres Rechners. Ohne VS Code verbinden Sie sich mit `viper ssh <rechner>`; VS Code öffnen Sie später mit `viper vscode <rechner>` erneut.

Bei den Anbietern:

* Ein Konto bei einem Cloud-Anbieter. Bei **Hetzner Cloud** ein Projekt mit einem **API-Token mit Lese- und Schreibrechten**, bei **AWS** eine Anmeldung (`aws login` oder SSO) mit Rechten für EC2; Google Cloud und Azure siehe Kapitel 4.
* Eine Domain bei Cloudflare, wenn `viper` den DNS-Namen der Rechner anlegen soll (Standard). Dafür brauchen Sie einen **API-Token** mit den Rechten **Zone DNS Edit** und **Zone DNS Read** und die **Zone-ID** der Domain. Ohne Domain und Cloudflare arbeiten Sie mit `DNS_PROVIDER=none` (Kapitel 4): Dann erreichen Sie die Rechner über ihre Adresse.

> **Kosten:** `viper` legt kostenpflichtige Ressourcen an. Der Anbieter kann zusätzlich die öffentliche IPv4-Adresse berechnen (Hetzner, AWS). Ein gelöschter Server verursacht keine weiteren Kosten.

> **Datenverlust:** Es gibt keinen separaten Datenträger. Die Arbeitsdateien liegen auf der Platte des Servers und gehen beim Löschen verloren. Was nicht gepusht ist, ist weg.

> **Sicherheit:** Legen Sie API-Tokens nie in Git ab. Der Benutzer `builder` auf dem Server hat Root-Rechte (sudo ohne Passwort, Docker-Gruppe). Arbeiten Sie dort nur mit vertrauenswürdigen Repositories.

## 2. Erste Schritte

### Überblick

| Befehl | Aufgabe |
|---|---|
| `viper setup` | Legt Ihre `.env.local` (aus einer vorbereiteten Vorlage), Ihren Login-Schlüssel und die lokale Host-CA an (einmalig). |
| `viper preflight <rechner>` | Prüft vorab nur lesend Einstellungen, Setup, Ihren Cloud-Anbieter, Cloudflare (falls verwendet) und GitHub. Legt nichts an. `viper up` führt es zuerst aus. |
| `viper up <rechner>` | Richtet einen Rechner ein, setzt den DNS-Eintrag, legt das Arbeitsverzeichnis an und klont das Repository. |
| `viper check <rechner>` | Prüft einen laufenden Rechner nur lesend: DNS, Zugriff über den Namen wie ein anderes Gerät, Zustand des Servers. |
| `viper ssh <rechner>` | Öffnet eine Shell auf dem Rechner oder führt mit `--cmd` einen Befehl aus, mit der SSH-Konfiguration des Rechners. Mit `--tmux` hängt sich die Verbindung an die tmux-Sitzung `claude` an (oder legt sie im Arbeitsverzeichnis an), dieselbe wie in den VS-Code-Terminals; `--tmux` braucht ein Terminal und lässt sich nicht mit `--cmd` kombinieren. |
| `viper vscode <rechner>` | Öffnet VS Code (Remote-SSH) auf dem laufenden Rechner im Arbeitsverzeichnis, so wie `viper up` es nach dem Einrichten tut. Braucht den Befehl `code`. Die Terminals dort hängen sich an die tmux-Sitzung `claude`. |
| `viper down <rechner>` | Prüft den Git-Stand, warnt vor ungesicherter Arbeit und löscht Server und DNS-Eintrag. |
| `viper dns <rechner>` | Gibt den DNS-Namen des Rechners aus (`<SERVER_PREFIX>-<rechner>.<DNS_DOMAIN>`), ohne Zugriff auf den Anbieter oder Cloudflare. Mit `DNS_PROVIDER=none` gibt es keinen Namen; der Befehl nennt die zuletzt gespeicherte Adresse. |
| `viper find` | Sucht bei allen Anbietern Server-Typen, die mindestens die gewünschten CPUs, den RAM und die Platte haben, und zeigt die günstigsten mit Preis. Liest nur und braucht keinen Rechner. |
| `viper log [<rechner>]` | Zeigt die lokalen Protokolle: Ereignisse aller Rechner, eines Rechners oder mit `--full` dessen vollständige Ausgabe. |
| `viper help`, `viper version` | Zeigt alle Befehle bzw. die Version. |

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

* Linux oder macOS.
* Der OpenSSH-Client mit `ssh`, `ssh-keygen`, `ssh-keyscan` und `scp`. `viper` meldet es, bevor es etwas anlegt, wenn ein Programm fehlt.

#### Vorgehen

**Mit Homebrew** (Linux und macOS): `brew install pt9912/viper/viper`, dann `viper version` (Update: `brew upgrade viper`). Sonst von Hand:

1. Laden Sie von der Release-Seite (`https://github.com/pt9912/hetzner-viper/releases`) die Datei für Ihr System, zum Beispiel `viper_v0.12.0_linux_amd64` (`linux` oder `darwin`; `amd64` oder `arm64`, bei Apple Silicon `arm64`; für Windows gibt es nur `viper_v0.12.0_windows_amd64.exe`, siehe Hinweis zu Windows unten), und `SHA256SUMS`.
2. Prüfen Sie die Datei und machen Sie sie ausführbar:
   ```bash
   grep viper_v0.12.0_linux_amd64 SHA256SUMS | sha256sum --check      # macOS: ... | shasum -a 256 --check
   chmod +x viper_v0.12.0_linux_amd64
   mv viper_v0.12.0_linux_amd64 ~/bin/viper            # ein Verzeichnis in Ihrem PATH
   ```
3. Prüfen Sie die Installation: `viper version`.

#### Ergebnis

`viper help` listet alle Befehle. Eine neuere Version installieren Sie genauso; Ihre Einstellungen und Rechner bleiben unberührt, denn sie liegen in Ihrer `.env.local` und in `~/.local/state/hetzner-claude`.

#### Hinweise

* **Arbeitsverzeichnis:** `viper` liest `.env.local` (und `machines/<rechner>.env.local`) im aktuellen Verzeichnis oder in dem, das Sie mit `--dir <verzeichnis>` angeben. `viper setup` legt eine fehlende `.env.local` aus einer eingebauten Vorlage an. Ein relativer `SSH_KEY` gilt relativ zu diesem Verzeichnis.
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

Die Meldung `Ready: ssh hetzner-claude-web (work dir: …)` erscheint. Das Repository liegt im Arbeitsverzeichnis unter dem Arbeitsverzeichnis (Standard `/Development`).

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
* Der Weg über das eigene Konto (Azure-Subscription, Google-Projekt) ist für `viper find` noch nicht in einem echten Konto getestet. Die öffentlichen Quellen (AWS, Azure, Google Cloud) sind gegen die echten Dateien und Seiten geprüft.

### Protokolle ansehen

`viper up` (`viper up`), `viper down` (`viper down`), `viper check` (`viper check`) und ein eigenständig aufgerufenes `viper preflight` schreiben zwei Protokolle in `~/.local/state/hetzner-claude/` (Rechte `600`, ohne Tokens):

* `events.log`: eine Zeile pro Ereignis mit Zeit (lokale Zeit mit Abstand zu UTC, etwa `2026-10-21T15:12:03+02:00`), Server, Aktion und Ergebnis, etwa `server created id=…`, `ready`, `deleted`, `cancelled (unsaved work)`, `interrupted` (Strg-C, geschlossenes Terminal) oder `FAILED (exit 1; …)`. Daraus sehen Sie, welche Server wann liefen.
* `logs/<SERVER_PREFIX>-<rechner>.log`: die vollständige Ausgabe jedes Laufs, Läufe getrennt durch eine Zeile `===== <Zeit> <aktion> <server>`. Am Ende jedes `viper up` steht dort unter `== installed on the server ==`, was auf dem Server installiert ist: Betriebssystem und Kernel, die apt-Pakete aus `cloud-init.yaml` mit Version, Claude Code, `gh`, Docker sowie Homebrew mit Taps, vertrauten Einträgen und den angeforderten Formeln samt Version. Das Protokoll von Homebrew selbst (`~/.config/viper/brew-packages.log`) bleibt auf dem Server.

#### Vorgehen

* Alle Ereignisse (die letzten 50): `viper log`
* Ereignisse eines Rechners: `viper log <rechner>`
* Vollständige Ausgabe eines Rechners: `viper log <rechner> --full`

`viper log` braucht keine Tokens. Eine Repo-URL mit Zugangsdaten (`https://user:token@…`) lehnt der Preflight ab, damit das Token weder in die Protokolle noch auf den Server gelangt. Die Protokolle werden nicht automatisch gekürzt; löschen Sie sie bei Bedarf von Hand. Protokolle auf dem Server (cloud-init, Homebrew) verschwinden dagegen mit dem Server.

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

Jeder Rechner hat einen eigenen Server, eine eigene Platte und einen eigenen DNS-Namen. Sie beenden sie getrennt mit `viper down web` und `viper down api`.

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
| `PROVIDER` | Cloud-Anbieter: `hetzner`, `aws`, `gcp` oder `azure`; wird nur aus den Einstellungsdateien gelesen, nicht aus der Shell | `hetzner` |
| `SERVER_TYPE` | Servertyp (Hetzner), Instanztyp (AWS), Maschinentyp (GCP) bzw. VM-Größe (Azure) | `cpx32`, `t3.large`, `e2-standard-2` bzw. `Standard_D2s_v5` |
| `SERVER_LOCATION` | Standort (Hetzner), Region oder Zone (AWS, z. B. `eu-central-1b`), Zone (GCP, z. B. `europe-west3-b`) bzw. Region (Azure, z. B. `germanywestcentral`) | `fsn1`, `eu-central-1`, `europe-west3-b` bzw. `germanywestcentral` |
| `SERVER_IMAGE` | Betriebssystem-Image; bei AWS, GCP und Azure `ubuntu-<Version>` (AWS auch `ami-…`, GCP auch `projects/…`, Azure auch `Herausgeber:Angebot:SKU:Version`) | `ubuntu-26.04` (Hetzner), sonst `ubuntu-24.04` |
| `AWS_PROFILE` | AWS-Profil für die Anmeldung (nur AWS) | Standardkette |
| `AWS_SUBNET_ID`, `AWS_VPC_ID` | Subnetz bzw. VPC für den Server (nur AWS) | Default-VPC |
| `AWS_ROOT_GB` | Größe der Systemplatte in GB (nur AWS) | `50` |
| `AWS_TAGS` | Zusätzliche Tags für Instanz und Systemplatte als kommagetrennte `SCHLÜSSEL=WERT`-Paare, z. B. `owner=me,ttl=2026-11-01T12:00:00Z` (nur AWS) | leer |
| `AWS_TAGS_SHARED` | Zusätzliche Tags für Schlüsselpaar und Security Group, gleiches Format wie `AWS_TAGS` (nur AWS) | leer |
| `GCP_PROJECT` | Google-Cloud-Projekt (nur GCP, Pflichtangabe) | leer |
| `GCP_NETWORK`, `GCP_ROOT_GB` | VPC-Netz bzw. Größe der Bootplatte in GB (nur GCP) | `default` bzw. `50` |
| `AZURE_SUBSCRIPTION_ID` | Azure-Abonnement (nur Azure, Pflichtangabe) | leer |
| `AZURE_RESOURCE_GROUP`, `AZURE_SUBNET_ID`, `AZURE_ROOT_GB` | Ressourcengruppe, vorhandenes Subnetz bzw. Größe der Systemplatte in GB (nur Azure) | `viper`, eigenes Netz bzw. `64` |
| `DNS_PROVIDER` | Wer den DNS-Namen anlegt: `cloudflare` oder `none` (kein Eintrag, keine Domain, kein Cloudflare); wird nur aus den Einstellungsdateien gelesen | `cloudflare` |
| `DNS_DOMAIN` | Ihre Domain; Pflichtangabe mit Cloudflare | leer |
| `SSH_KEY` | Pfad Ihres Login-Schlüssels | `~/.ssh/hetzner-build` |
| `SSH_ALLOWED_CIDRS` | Netze, die Port 22 erreichen dürfen | `0.0.0.0/0,::/0` |
| `IPAD_SSH_PUBLIC_KEY_FILE` | Öffentlicher Schlüssel des iPads | leer |
| `VIPER_HOMEBREW` | Homebrew (`brew`) auf dem Server installieren: `1` oder `0` | `1` |
| `VIPER_BREW_TAPS` | Homebrew-Taps (`owner/repo` oder `owner/repo=URL`), mit Leerzeichen getrennt | leer |
| `VIPER_BREW_TRUST` | Einträge für `brew trust`: `owner/repo` (Tap), `owner/repo/name` (Formel), `cask:…`, `command:…` | leer |
| `VIPER_BREW_PACKAGES` | Zu installierende Formeln (`name` oder `owner/repo/name`) | leer |
| `VIPER_BREWFILE` | `1`: eine `Brewfile` im geklonten Repository mit `brew bundle` anwenden | `0` |
| `VIPER_POST_CLONE` | Shell-Befehle, die nach dem Clone und nach Homebrew im geklonten Repository laufen (mehrere Zeilen: Wert in einfachen Anführungszeichen, dann kommen `$VARIABLEN` unverändert auf dem Server an; in doppelten würden sie auf Ihrem Computer aufgelöst). Brauchen ein Repository, laufen nur einmal direkt nach dem Clone (nach einem Fehler versucht es das nächste `viper up` erneut), ein Fehler ist standardmäßig nur eine Warnung (siehe `VIPER_POST_CLONE_ON_ERROR`). Log auf dem Server: `~/.config/viper/post-clone.log` | leer |
| `VIPER_POST_CLONE_ON_ERROR` | Bei einem Fehler der Befehle von `VIPER_POST_CLONE`: `retry` (Warnung, das nächste `viper up` versucht es erneut), `ignore` (Warnung, nie wieder) oder `abort` (`viper up` schlägt fehl, der Rechner bleibt, das nächste `viper up` versucht es erneut). Der Ausgang steht im Protokoll (`viper log`) | `retry` |
| `WORKSPACE_DIR` | Verzeichnis auf dem Server, in das die Repositories geklont werden | `/Development` |
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

### Google Cloud und Azure als Anbieter (nicht in einem echten Konto getestet)

Mit `PROVIDER=gcp` bzw. `PROVIDER=azure` richtet `viper up` den Server bei Google Compute Engine bzw. Microsoft Azure ein. **Diese beiden Anbieter sind gebaut und gegen nachgebildete Schnittstellen getestet, aber noch nicht in einem echten Konto ausprobiert.** Prüfen Sie zuerst mit `viper preflight <rechner>` und einem kleinen Server, und rechnen Sie mit Fehlern. Hetzner und AWS sind dagegen in echten Konten getestet.

* **Google Cloud:** Anmeldung mit `gcloud auth application-default login` (oder einer Dienstkonto-Datei in `GOOGLE_APPLICATION_CREDENTIALS`). `GCP_PROJECT` ist Pflicht. Der Server ist eine VM in der Zone aus `SERVER_LOCATION` im Netz `default` (oder `GCP_NETWORK`) mit öffentlicher Adresse. Die Firewall besteht aus zwei VPC-Regeln `<SERVER_PREFIX>-ssh` (IPv4) und `<SERVER_PREFIX>-ssh-ipv6`, die für VMs mit dem Netzwerk-Tag `<SERVER_PREFIX>-ssh` gelten. Die Rolle braucht Rechte für Compute-Instanzen und Firewall-Regeln.
* **Azure:** Anmeldung mit `az login`. `AZURE_SUBSCRIPTION_ID` ist Pflicht. `viper` legt in der Ressourcengruppe (`viper`) ein virtuelles Netz `viper-vnet` mit Subnetz, eine Netzwerksicherheitsgruppe `<SERVER_PREFIX>-ssh` sowie je Server eine öffentliche Adresse, eine Netzwerkkarte und eine VM an. Alles trägt das Tag `viper`. `viper down` löscht VM, Netzwerkkarte, Adresse und Platte; Gruppe, Netz und Sicherheitsgruppe bleiben.
* **Schlüssel:** Beide Anbieter kennen keine Schlüsselobjekte; der Login-Schlüssel geht mit dem Server (cloud-init).

### Firewall und Anmeldung

* Die Firewall erlaubt eingehend SSH (TCP-Port 22) und ICMP (`ping`), beides aus den Netzen in `SSH_ALLOWED_CIDRS`. Fehlt eine der beiden Regeln, ergänzt `viper up` sie, auch in einer schon vorhandenen Firewall. Andere Regeln lässt es unverändert. Eine bestehende Regel ändert es nicht: Wollen Sie die Netze nachträglich einschränken, ändern Sie die Regeln in der Hetzner Console (bei AWS: an der Security Group in der EC2-Konsole).
* Die SSH-Konfiguration jedes Rechners verbietet Agent- und X11-Weiterleitung (`ForwardAgent no`, `ForwardX11 no`), auch wenn Ihre `~/.ssh/config` sie allgemein erlaubt. Der Agent auf dem Server hat Root-Rechte und soll Ihre lokalen SSH-Schlüssel nicht benutzen können.
* Standardmäßig ist SSH aus dem ganzen Internet erreichbar. Anmeldung ist nur mit SSH-Schlüssel möglich. Passwörter und Root-Login sind abgeschaltet. Schränken Sie die Netze ein, wenn Sie nur von festen Adressen arbeiten. Eine Alternative ist ein VPN.
* Ausgehende Verbindungen des Servers sind nicht eingeschränkt.

### DNS

Der DNS-Eintrag ist ein A-Record ohne Cloudflare-Proxy, weil dieser SSH nicht weiterleitet. `viper` ändert oder löscht nur Einträge, die es selbst angelegt hat (Kommentar `managed by hetzner-claude`).

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

## 9. Glossar

| Begriff | Bedeutung |
|---|---|
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

## 10. Support und Kontakt

Zu `viper` gibt es keinen eigenen Support. Prüfen Sie bei Problemen zuerst Kapitel 7. Für Ihren Cloud-Anbieter und Cloudflare gelten deren Dokumentation und Support. Beachten Sie: Echt gelaufen sind Einrichtung, Wiederverwenden, Neuanlegen, Check und Löschen auf Hetzner Cloud mit Cloudflare-DNS sowie Claude über das Abo-Token und Ollamas Cloud, aber mit den früheren Bash-Skripten, auf denen `viper` aufbaut. Das Go-Programm `viper` ist auf **AWS** in echten Läufen getestet: Vorabprüfung, Einrichtung mit `cloud-init` und SSH und Löschen, auf x86 und ARM, ohne Cloudflare (`DNS_PROVIDER=none`). Auf Hetzner, mit Cloudflare, bei Google Cloud und bei Azure ist es nur gegen nachgebildete Schnittstellen getestet, nicht gegen die echten APIs. Noch nicht echt gelaufen sind außerdem `VIPER_GH_TOKEN`/gh, Deploy-Keys, das iPad und ein vLLM-Server. Probieren Sie Neues in einem Testprojekt und mit `viper preflight` aus.

## 11. Änderungshistorie

| Handbuch | Software | Änderung für Sie |
|---|---|---|
| 1.0 | v3 mit Mehrrechner-Betrieb | Mehrere Rechner mit eigenem Namen, automatischer Git-Clone beim Start, Einstellungen in `.env.local`, Beenden mit `--force`. |
| 1.1 | v3 mit Mehrrechner-Betrieb | Ausführung im Container mit `make up` und `make down`. Behoben: Auf einem frischen System fehlte die SSH-Einbindung in `~/.ssh/config`. |
| 1.2 | v3 mit Mehrrechner-Betrieb | Kein separater Datenträger mehr: Die Dateien liegen auf der Serverplatte und werden mit dem Server gelöscht. `stop.sh` warnt vor ungepushter Arbeit. |
| 1.3 | v3 mit Mehrrechner-Betrieb | `stop.sh` fragt bei ungesicherter Arbeit zusätzlich nach `DISCARD`. |
| 1.4 | v3 mit Mehrrechner-Betrieb | `start.sh` akzeptiert eine Einrichtung mit Warnungen und meldet „Ready“ nur nach erfolgreicher Installation von Claude Code. `stop.sh` erkennt auch Stash-Einträge, listet ignorierte Dateien auf und räumt DNS und SSH-Konfiguration auch bei schon gelöschtem Server auf. Schlüsseldateien werden vor dem Anlegen geprüft. |
| 1.5 | v3 mit Mehrrechner-Betrieb | GitHub-CLI `gh` auf jedem Rechner. Ein PAT aus `GH_TOKEN` meldet `gh` an und wird für `git` über HTTPS genutzt. |
| 1.6 | v3 mit Mehrrechner-Betrieb | Standard-Image ist Ubuntu 26.04 LTS. Bestehende `.env.local` mit `ubuntu-24.04` funktionieren weiter. |
| 1.7 | v3 mit Mehrrechner-Betrieb | Klonen ohne `REPO_BRANCH` funktioniert. `stop.sh` erkennt ungepushte Commits auf allen Branches und in Worktrees und prüft das beim Start genutzte Arbeitsverzeichnis. Fehler der Hetzner-API führen zum Abbruch statt zum Aufräumen. Verbindung über die IP, keine Wartezeit auf DNS. Nach Ablauf des Zertifikats bleibt die Verbindung über den hinterlegten Host-Schlüssel möglich. Tokens erscheinen nicht mehr in Befehlszeilen und werden nicht an VS Code vererbt. |
| 1.8 | v3 mit Mehrrechner-Betrieb | DNS-Name und lokale Dateien enthalten das Präfix (`claude-web.<domain>`). Die Skripte vertrauen nur dem hinterlegten Host-Schlüssel des jeweiligen Servers. `stop.sh` sucht Repositories auch im Home- und Temp-Verzeichnis, meldet Dateien außerhalb von Repositories und fragt vor dem Aufräumen eines nicht gefundenen Servers nach `CLEANUP`. `start.sh` holt eine fehlgeschlagene Claude-Installation nach und verwendet einen schon hochgeladenen SSH-Schlüssel weiter. Antworten können per Pipe übergeben werden. |
| 1.9 | v3 mit Mehrrechner-Betrieb | Das GitHub-Token heißt jetzt `VIPER_GH_TOKEN` (statt `GH_TOKEN`), ein lokal exportiertes `GH_TOKEN` wird nicht mehr übernommen. Ein abgelehnter Host-Schlüssel wird als solcher gemeldet statt als „nicht erreichbar“, und `stop.sh --force` prüft einen erreichbaren Server trotzdem. Hinterlegte Schlüssel werden bei neuer Server-ID verworfen und nach einer CA-geprüften Verbindung nachgetragen. Eine als Symlink verwaltete `~/.ssh/config` bleibt erhalten. Die Bestellbarkeit wird über die Standortangaben des Servertyps geprüft (die frühere Rechenzentrums-Abfrage hat Hetzner abgeschaltet). Neu: Abschnitt „Erster Testlauf mit einem kleinen Server“ und `make ssh` (bzw. `connect.sh`) für Verbindungen ohne lokalen SSH-Client; das Handbuch nennt jetzt durchgehend die `make`-Befehle. `preflight.sh` prüft vorab nur lesend, und `start.sh` führt es zuerst aus. Fehlende Pflichtwerte werden gemeinsam gemeldet, bevor etwas angelegt oder gelöscht wird. |
| 1.10 | v3 mit Mehrrechner-Betrieb | `make` ohne Ziel zeigt eine Übersicht aller Befehle (`make help`) statt die Tests zu starten. |
| 1.11 | v3 mit Mehrrechner-Betrieb | Mit `VIPER_CLAUDE_TOKEN` (aus `claude setup-token`) ist Claude Code auf dem Server ohne erneute Anmeldung nutzbar. |
| 1.12 | v3 mit Mehrrechner-Betrieb | `make check` prüft einen laufenden Rechner: DNS bei öffentlichen Resolvern, Zugriff über den Namen mit der Host-CA wie ein anderes Gerät (mit Ablaufdatum des Zertifikats) und den Zustand des Servers. |
| 1.13 | v3 mit Mehrrechner-Betrieb | Die Firewall erlaubt zusätzlich ICMP (`ping`); fehlende Regeln werden auch in einer vorhandenen Firewall ergänzt. `make check` benennt den gefragten Resolver und erklärt zwischengespeicherte negative DNS-Antworten. |
| 1.14 | v3 mit Mehrrechner-Betrieb | `stop.sh` prüft alle Arbeitsverzeichnisse, mit denen ein Rechner je gestartet wurde. SSH-Adressen ohne Deploy-Key: mit `VIPER_GH_TOKEN` über HTTPS geklont, sonst vom Preflight abgelehnt. Keine Agent- oder X11-Weiterleitung zum Server. Ein Symlink als `~/.ssh/config` funktioniert im Container. Beim Wiederverwenden eines Servers enden Host-Key- und Anmeldefehler sofort. Klassische GitHub-Tokens mit `admin:org`/`write:org` werden akzeptiert. |
| 1.15 | v3 mit Mehrrechner-Betrieb | Andere Modelle über eine Anthropic-kompatible Schnittstelle (Ollama-Cloud, vLLM, Gateway): `VIPER_ANTHROPIC_BASE_URL`, `VIPER_ANTHROPIC_AUTH_TOKEN`, `VIPER_CLAUDE_MODEL`; der Preflight testet Schlüssel und Modell. |
| 1.16 | v3 mit Mehrrechner-Betrieb | Neue Server spielen beim Einrichten alle Updates ein und starten bei Bedarf einmal neu; `start.sh` wartet darauf, `make check` warnt bei später nötigem Neustart. Interaktive Shells starten im Arbeitsverzeichnis. cloud-init schaltet sich nach der Ersteinrichtung ab (sonst Fehlermeldungen nach jedem Neustart). `make ssh CMD=…` und `make up REPO=…` übernehmen `$` unverändert. |
| 1.17 | v3 mit Mehrrechner-Betrieb | Ein abgebrochener erster `make up` wird beim erneuten Lauf wie ein neuer Server behandelt. `stop.sh` meldet einen abgelehnten Login-Schlüssel, kennt auch Arbeitsverzeichnisse anderer Computer und meldet Remotes auf dem Server selbst. `make ssh CMD=…` fordert im Terminal ein Terminal an (`tmux attach`). Ein Deploy-Key wird nicht mehr durch `VIPER_GH_TOKEN` umgangen. Der Check liest Fehler der Ersteinrichtung und akzeptiert nur eine echte OK-Antwort. Ein privater Schlüssel als iPad-Schlüssel wird abgelehnt. |
| 1.18 | v3 mit Mehrrechner-Betrieb | Homebrew (`brew`) wird auf jedem neuen Server installiert (abschaltbar mit `VIPER_HOMEBREW=0`); `make check` prüft es. |
| 1.19 | v3 mit Mehrrechner-Betrieb | Werkzeuge mit Homebrew: `VIPER_BREW_TAPS`, `VIPER_BREW_TRUST`, `VIPER_BREW_PACKAGES` und eine `Brewfile` im Repository; Reihenfolge Tap, Trust, Installation; Preflight prüft das Format, `make check` den Stand. |
| 1.20 | v3 mit Mehrrechner-Betrieb | Protokolle: `events.log` (ein Ereignis pro Zeile) und `logs/<server>.log` (vollständige Ausgabe) in `~/.local/state/hetzner-claude/`; Anzeige mit `make log`. |
| 1.21 | v3 mit Mehrrechner-Betrieb | `stop.sh` findet neue Dateien unabhängig von Git-Einstellungen, meldet ausgeblendete Dateien und Commits nur hinter Tags und löscht nichts bei unvollständiger Prüfung. Eingerichtet-Vermerk auch auf dem Server; Warten auf einen Neustart höchstens etwa drei Minuten. `Brewfile` nur mit `VIPER_BREWFILE=1`. Protokolle: `interrupted`, keine Zugangsdaten aus Repo-URLs (abgelehnt), Bestandsliste des Servers nach jedem `make up`. `FORCE`/`FULL` nur mit `1`. |
| 1.22 | v3 mit Mehrrechner-Betrieb | Protokolle mit lokaler Uhrzeit und Abstand zu UTC; der `make`-Container übernimmt die Zeitzone des Rechners. |
| 1.23 | v3 mit Mehrrechner-Betrieb | Ein per `REPO=` angegebenes Repository wird für den Rechner gemerkt. Tags aus dem Clone gelten nicht als ungesicherte Arbeit. Zugangsdaten in Tap-URLs werden abgelehnt, Adressen mit Zugangsdaten nirgends angezeigt. Ein älterer Server gilt auch auf anderen Computern als eingerichtet. Die Zeitzone des Rechners wird im Container sauber übernommen. |
| 1.24 | v3 mit Mehrrechner-Betrieb | Schritt-für-Schritt-Anleitung und Rechtetabelle für das fein granulare GitHub-Token; der Preflight prüft, ob `VIPER_GH_TOKEN` in das Repository pushen darf. |
| 1.25 | v3 mit Mehrrechner-Betrieb | Standard-Servertyp ist jetzt `cpx32` (4 vCPU, 8 GB RAM) statt `cx53`; `stop.sh` fragt nicht nach `CLEANUP`, wenn nichts mehr übrig ist. `make log MACHINE=…` liest `SERVER_PREFIX` wie die Skripte, auch mit Kommentar hinter dem Wert. |
| 1.26 | v3 mit Mehrrechner-Betrieb | Neue Server legen eine `~/.tmux.conf` an (Maus, Fenster ab 1, Alt+1 bis Alt+9). |
| 1.27 | v3 mit Mehrrechner-Betrieb | Docker aus Dockers eigenem apt-Repository (neueste Engine, `buildx`, `compose`) statt Ubuntus `docker.io`. Git-Autor auf dem Server: `VIPER_GIT_USER_NAME`/`VIPER_GIT_USER_EMAIL`, sonst vom GitHub-Konto. Optional `code-server` (VS Code im Browser, nur per SSH-Tunnel): `VIPER_CODE_SERVER`, `VIPER_CODE_SERVER_PASSWORD`. |
| 1.28 | v3 mit Mehrrechner-Betrieb | Handbuch: `code-server` von einem anderen Rechner aus nutzen (Tunnel mit DNS-Namen, Schlüssel, Host-CA). |
| 1.29 | v3 mit Mehrrechner-Betrieb | Die Installation von Claude Code wird bei Fehlern (etwa HTTP 429, Drosselung pro Adresse) bis zu fünfmal mit wachsender Pause wiederholt, auf neuen Servern in cloud-init und beim Nachholen in `start.sh`. |
| 1.30 | v3 mit Mehrrechner-Betrieb | Optional Swap-Datei auf dem Server: `VIPER_SWAP` als Faktor vom RAM (`1`, `0.5`) oder als Größe (`4G`). `make check` prüft sie. |
| 1.31 | v3 mit Mehrrechner-Betrieb | `make build BASE_IMAGE=…` baut den Container mit einer anderen Quelle für das Basis-Image (etwa `mirror.gcr.io`), wenn Docker Hub nicht erreichbar ist. |
| 1.32 | v3 mit Mehrrechner-Betrieb | `make build` prüft vorab, ob Docker Hub per IPv4 erreichbar ist, und baut sonst automatisch aus `mirror.gcr.io`; `BASE_IMAGE` überschreibt die Quelle. |
| 1.33 | v0.1.0 | Erstes Release `v0.1.0` (nur Quellcode); die Software wird ab jetzt nach Release-Version statt als „v3“ benannt. Neue Aufgabe „Eine bestimmte Version verwenden“. |
| 1.34 | v0.2.0 | Zu jedem Release gehört ein Container-Image (`ghcr.io/pt9912/hetzner-viper`), das `make` auf einem Release-Tag selbst holt, statt es zu bauen. Das Image ist nicht mehr an Ihre Benutzerkennung gebunden. |
| 1.35 | v0.4.0 | Neues Programm `viper` (eine Datei, nur Docker nötig) ersetzt `make` für den Container-Betrieb; Binaries für Linux und macOS am Release. Die erzeugte `/etc/passwd` liegt jetzt im Zustandsverzeichnis, damit `make` auch mit einer VM wie Colima funktioniert. |
| 1.36 | v0.5.0 | Alle Befehle von `viper` laufen direkt auf Ihrem Rechner, ohne Container, Docker und Klon (nur OpenSSH nötig). `.env.local` wird von `viper` selbst gelesen. |
| 1.37 | v0.5.0 | Die Bash-Skripte, `make` und das Container-Image entfallen; `viper` ist der einzige Weg. Dieses Handbuch beschreibt nur noch `viper`; die älteren Zeilen dieser Tabelle nennen die damaligen Skripte und `make`-Ziele. |
| 1.38 | v0.5.0 | Entwicklung: `make build` baut `viper` in Docker (`dist/viper`), `make check` führt gofmt, `go vet`, golangci-lint und alle Tests in Containern aus (`make help` listet die Ziele). Für die Benutzung ändert sich nichts. |
| 1.39 | v0.5.0 | Neue Option `viper up <rechner> --no-code`: VS Code wird nach dem Start nicht geöffnet. |
| 1.40 | v0.5.0 | Release `v0.5.0` mit den `viper`-Binaries für Linux und macOS. |
| 1.41 | v0.5.1 | `viper setup` legt zusätzlich `machines/example.env.local.example` an (Vorlage für die Einstellungen eines Rechners). Die Meldung bei fehlender `.env.local` verweist auf `viper setup`. |
| 1.42 | v0.5.1 | `viper` lässt sich auch mit Homebrew installieren: `brew install pt9912/viper/viper`. |
| 1.43 | v0.5.2 | `viper help` nennt am Ende die Adresse dieses Handbuchs (der installierten Version und der aktuellen auf `main`). |
| 1.44 | v0.5.2 | macOS gilt nicht mehr als „nicht erprobt“: `viper` startet dort. |
| 1.45 | v0.6.0 | Neue Server stellen für VS Code Remote-SSH ein tmux-Terminal-Profil ein: Die Shell überlebt das Schließen von VS Code. |
| 1.46 | v0.7.0 | Neu: `viper vscode <rechner>` öffnet VS Code auf einem laufenden Rechner erneut. |
| 1.47 | v0.7.0 | Neu: `viper ssh <rechner> --tmux` hängt sich an die tmux-Sitzung `claude` an. |
| 1.48 | v0.8.0 | Neu: Einstellung `PROVIDER` (Cloud-Anbieter). Die Zugangsdaten und Standardwerte hängen vom Anbieter ab. |
| 1.49 | v0.8.0 | Neu: AWS (Amazon EC2) als Anbieter, in einem echten Konto getestet (x86, ARM). Neue Einstellungen `AWS_PROFILE`, `AWS_SUBNET_ID`, `AWS_VPC_ID`, `AWS_ROOT_GB`. |
| 1.50 | v0.8.0 | Neu: Google Cloud (`gcp`) und Azure (`azure`) als Anbieter, noch nicht in einem echten Konto getestet. |
| 1.51 | v0.8.0 | Neu: `DNS_PROVIDER=none` für den Betrieb ohne Cloudflare und ohne Domain. Die Kapitel zu Voraussetzungen, Einrichtung, Preflight und Fehlerbehebung gelten nun für alle Anbieter; die Plattengröße (`*_ROOT_GB`) wird geprüft. |
| 1.52 | v0.8.0 | Release v0.8.0: die Änderungen 1.48 bis 1.51 (Anbieter AWS, Google Cloud und Azure, Betrieb ohne Cloudflare). Die Dateinamen der Binaries im Abschnitt „`viper` installieren“ nennen jetzt `v0.8.0`. |
| 1.53 | v0.9.0 | Neu: Einstellungen `AWS_TAGS` und `AWS_TAGS_SHARED` (nur AWS): zusätzliche Tags für Instanz und Systemplatte bzw. für Schlüsselpaar und Security Group beim Anlegen; `viper preflight` prüft Syntax und AWS-Grenzen. |
| 1.54 | v0.9.0 | Release v0.9.0: die Änderung 1.53 (`AWS_TAGS`, `AWS_TAGS_SHARED`). Die Dateinamen der Binaries im Abschnitt „`viper` installieren“ nennen jetzt `v0.9.0`. |
| 1.55 | v0.10.0 | Neu: Binary für Windows (`windows_amd64.exe`), nur teilweise getestet; empfohlen bleibt WSL mit dem Linux-Binary. Pfade in `.env.local` stehen dort mit `/`. |
| 1.56 | v0.10.0 | Release v0.10.0: die Änderung 1.55 (Windows-Binary). Die Dateinamen der Binaries im Abschnitt „`viper` installieren“ nennen jetzt `v0.10.0`. |
| 1.57 | v0.11.0 | Neu: `VIPER_POST_CLONE` führt Shell-Befehle einmalig direkt nach dem Clone im Repository auf dem Server aus (nach Homebrew); `VIPER_POST_CLONE_ON_ERROR=retry`, `ignore` oder `abort` bestimmt das Verhalten bei einem Fehler, der Ausgang steht im Protokoll. |
| 1.58 | v0.11.0 | Release v0.11.0: die Änderung 1.57 (`VIPER_POST_CLONE`). Die Dateinamen der Binaries im Abschnitt „`viper` installieren“ nennen jetzt `v0.11.0`. |
| 1.59 | v0.12.0 | Neu: `viper find` sucht Server-Typen nach CPUs, RAM und Platte und zeigt die günstigsten mit Preis (Hetzner: Euro inklusive Mehrwertsteuer, ohne IPv4); zunächst nur für Hetzner. |
| 1.60 | v0.12.0 | `viper find` fragt jetzt auch AWS, Azure und Google Cloud ab (gleichzeitig), rechnet die getrennt gewählte Systemplatte in den Preis ein und kennt `--provider`. AWS braucht dafür keine Anmeldung: `viper find` liest die öffentliche Preisliste und hält sie einen Tag im Cache (`~/.cache/viper`). |
| 1.61 | v0.12.0 | Korrektur zu 1.60: Die Preisliste von AWS kommt aus der öffentlichen Preisdatei mit Tages-Cache statt aus der API; kein AWS-Konto nötig. |
| 1.62 | v0.12.0 | `viper find` zeigt alle Anbieter in einer nach Preis sortierten Tabelle und rechnet sie auf eine Basis um: Euro (AWS mit dem Kurs der EZB) und derselbe Mehrwertsteuersatz (`--vat`). Azure braucht keine Subscription mehr (VM-Größen aus den öffentlichen Daten des Preisrechners, sonst aus der Subscription), und die Preislisten von Azure liegen wie die von AWS einen Tag im Cache. Ein abgebrochener AWS-Download wird wiederholt. |
| 1.63 | v0.12.0 | `viper find` braucht für Google Cloud keine Anmeldung und kein Projekt mehr: Es liest die öffentlichen Preisseiten (Cache einen Tag). Ein gesetztes `GCP_PROJECT` wird zuerst genutzt, bei einem Fehler gilt der Rückfall, und die Meldung nennt den Grund. |
| 1.64 | v0.12.0 | `viper find` über das Google-Projekt lässt Typen mit GPU aus (die GPU ist nicht im Preis). Hinweis zum anderen Umrechnungskurs der Google-Preise. |
| 1.65 | v0.12.0 | `viper find`: Euro-Zeilen stehen immer vor Zeilen in einer nicht umgerechneten Währung; Kurse der EZB gelten ohne Netz höchstens sieben Tage; ein Standort muss ein gültiger Name sein; eine geänderte Google-Seite führt zu einem Fehler statt zu einem unvollständigen Cache. |
| 1.66 | v0.12.0 | Release v0.12.0: die Änderungen 1.59 bis 1.65 (`viper find`). Die Dateinamen der Binaries im Abschnitt „`viper` installieren“ nennen jetzt `v0.12.0`. |
| 1.67 | v0.13.0 | `viper find` braucht für Hetzner keinen Token mehr (öffentliche Seiten von hetzner.com; mit Token weiter die Preise des Projekts) und rechnet bei allen Anbietern die Gebühr für die öffentliche IPv4-Adresse in den Preis. Über das Google-Projekt kommen die Preise jetzt in Dollar, wie über die Seiten. |
| 1.68 | v0.13.0 | `viper find`: `--location` braucht `--provider`; gemerkte Preise gelten ohne Netz höchstens sieben Tage (bisher nur der Kurs der EZB); Google: geteilte Kerne auch ohne Projekt nicht mehr in der Liste. |

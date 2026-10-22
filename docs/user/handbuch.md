# Benutzerhandbuch: Hetzner Viper

Version: 1.26  
Software-Version: v3 (mit Mehrrechner-Betrieb)  
Stand: 21.10.2026

## 1. Einleitung

### Zweck der Software

Die Skripte in diesem Repository richten mit einem Befehl einen entfernten Arbeitsrechner bei Hetzner Cloud ein. Auf diesem Rechner arbeitet der Code-Agent Claude Code als „virtuelle Person“ (VIPER: VIrtual PERson) in Ihrem Repository. Sie arbeiten darauf über VS Code (Remote-SSH) oder über ein SSH-Terminal, zum Beispiel vom iPad.

Pro Git-Repository richten Sie einen eigenen Rechner ein. Beim Einrichten wird das Repository automatisch geklont. Wenn Sie fertig sind, löschen Sie den Rechner wieder. Alle Dateien auf dem Rechner werden dabei gelöscht. Sichern Sie Ihre Arbeit deshalb vorher mit `git push`.

### Zielgruppe

Dieses Handbuch richtet sich an Entwicklerinnen und Entwickler, die auf einem Linux-Rechner arbeiten und die Kommandozeile bedienen können.

### Voraussetzungen

Auf Ihrem lokalen Rechner (nur Linux ist erprobt):

* **Empfohlen:** Docker und `make`. Alle Werkzeuge (`ssh`, `hcloud`, `jq`, `curl`, `python3`) kommen dann aus dem Container, Sie installieren sonst nichts. Dieses Handbuch nennt jeweils den `make`-Befehl (siehe auch Aufgabe „Rechner im Container einrichten und beenden“).
* **Alternativ** ohne Container: `bash`, `curl`, `jq`, `python3`, OpenSSH-Client und die Hetzner-Kommandozeile `hcloud`. Dann rufen Sie die Skripte direkt auf (`./setup.sh`, `./start.sh <rechner>` usw.).
* Optional VS Code mit der Erweiterung **Remote-SSH**. Sie nutzt den OpenSSH-Client Ihres Rechners, der dafür lokal installiert sein muss. Ohne ihn verbinden Sie sich mit `make ssh MACHINE=<rechner>`.

Bei den Anbietern:

* Ein Hetzner-Cloud-Projekt mit einem **API-Token mit Lese- und Schreibrechten**.
* Eine Domain bei Cloudflare. Dafür brauchen Sie einen **API-Token** mit den Rechten **Zone DNS Edit** und **Zone DNS Read** und die **Zone-ID** der Domain.

> **Kosten:** Die Skripte legen kostenpflichtige Ressourcen an. Hetzner kann zusätzlich die öffentliche IPv4-Adresse berechnen. Ein gelöschter Server verursacht keine weiteren Kosten.

> **Datenverlust:** Es gibt keinen separaten Datenträger. Die Arbeitsdateien liegen auf der Platte des Servers und gehen beim Löschen verloren. Was nicht gepusht ist, ist weg.

> **Sicherheit:** Legen Sie API-Tokens nie in Git ab. Der Benutzer `builder` auf dem Server hat Root-Rechte (sudo ohne Passwort, Docker-Gruppe). Arbeiten Sie dort nur mit vertrauenswürdigen Repositories.

## 2. Erste Schritte

### Überblick

| Skript | Aufgabe |
|---|---|
| `setup.sh` | Legt Ihren Login-Schlüssel und die lokale Host-CA an (einmalig). |
| `preflight.sh <rechner>` | Prüft vorab nur lesend Einstellungen, Setup, Hetzner, Cloudflare und GitHub. Legt nichts an. `start.sh` führt es zuerst aus. |
| `check.sh <rechner>` | Prüft einen laufenden Rechner nur lesend: DNS, Zugriff über den Namen wie ein anderes Gerät, Zustand des Servers. Im Container: `make check`. |
| `make log [MACHINE=…] [FULL=1]` | Zeigt die lokalen Protokolle: Ereignisse aller Rechner, eines Rechners oder dessen vollständige Ausgabe. |
| `connect.sh <rechner> [befehl]` | Öffnet eine Shell auf dem Rechner oder führt einen Befehl aus, mit der SSH-Konfiguration des Rechners. Im Container: `make ssh`. |
| `start.sh <rechner>` | Richtet einen Rechner ein, setzt den DNS-Eintrag, legt das Arbeitsverzeichnis an und klont das Repository. |
| `stop.sh <rechner>` | Prüft den Git-Stand, warnt vor ungesicherter Arbeit und löscht Server und DNS-Eintrag. |
| `dns.sh` | Verwaltet nur den eigenen DNS-Eintrag. Sie rufen es normalerweise nicht selbst auf. |

### Wie ein Rechner benannt wird

Jeder Rechner hat einen Namen, den Sie beim Start angeben (`<rechner>`). Erlaubt sind Kleinbuchstaben, Ziffern und Bindestriche. Der Name darf nicht mit einem Bindestrich beginnen oder enden. Aus dem Namen entstehen:

| Ressource | Name |
|---|---|
| Server | `<SERVER_PREFIX>-<rechner>` |
| DNS-Name | `<SERVER_PREFIX>-<rechner>.<DNS_DOMAIN>` |
| SSH-Alias | `hetzner-<SERVER_PREFIX>-<rechner>` |

Login-Schlüssel, Firewall und Host-CA teilen sich alle Rechner.

## 3. Aufgaben ausführen

### Erstmalige Einrichtung

#### Voraussetzung

Sie haben die Voraussetzungen aus Kapitel 1 erfüllt.

#### Vorgehen

1. Wechseln Sie in das Hauptverzeichnis des Repositorys.
2. Kopieren Sie die Vorlage: `cp .env.local.example .env.local`
3. Öffnen Sie `.env.local` und tragen Sie bei `DNS_DOMAIN` Ihre Domain ein.
4. Führen Sie `make setup` aus (ohne Container: `./setup.sh`).
5. Setzen Sie die Zugangsdaten in Ihrer Shell:
   ```bash
   export HCLOUD_TOKEN='...'
   export CLOUDFLARE_API_TOKEN='...'
   export CLOUDFLARE_ZONE_ID='...'
   ```
   Alternativ tragen Sie die drei Werte in `.env.local` ein (Zeilen mit `#` am Anfang freischalten).

#### Ergebnis

Es existieren Ihr Login-Schlüssel (Standard: `~/.ssh/hetzner-build`) und die Host-CA (`~/.ssh/hetzner_claude_host_ca`). Die Datei `.env.local` wird nicht in Git eingecheckt.

#### Hinweise

* Der private CA-Schlüssel darf nie auf den Server oder ein anderes Gerät kopiert werden.
* Die Login-Schlüssel haben keine Passphrase. Schützen Sie die Dateien im Ordner `~/.ssh`.

### Vorab prüfen (Preflight)

#### Voraussetzung

Die erstmalige Einrichtung ist abgeschlossen.

#### Vorgehen

1. Führen Sie `./preflight.sh <rechner> [repo-url]` aus, im Container `make preflight MACHINE=<rechner> [REPO=<url>]`.
2. Lesen Sie die Ausgabe. Jede Zeile beginnt mit `ok`, `info` oder `FAIL`.

Geprüft werden:

* **Einstellungen:** Alle Pflichtwerte sind gesetzt, `DNS_DOMAIN` und `SSH_ALLOWED_CIDRS` haben ein gültiges Format.
* **Lokales Setup:** Login-Schlüssel und Host-CA sind vorhanden, die CA gilt für `*.<DNS_DOMAIN>`, die Dateien aus `GIT_DEPLOY_KEY_FILE` und `IPAD_SSH_PUBLIC_KEY_FILE` sind lesbar.
* **Hetzner:** Das Token kann das Projekt lesen. Servertyp, Standort und Image (für die Architektur des Typs) existieren. Der Servertyp ist am Standort derzeit bestellbar. Der Login-Schlüssel kollidiert nicht mit einem anderen Schlüssel gleichen Namens.
* **Cloudflare:** Die Zone ist lesbar, `DNS_DOMAIN` liegt darin, und es gibt keinen fremden DNS-Eintrag für den Rechner.
* **GitHub (wenn `VIPER_GH_TOKEN` gesetzt ist):** Das Token ist gültig, ein klassisches Token hat die Scopes `repo` und `read:org`, und es hat Zugriff auf ein GitHub-Repository aus `REPO_URL` und darf dorthin pushen.
* **Repository:** Ein HTTPS- oder lokales Repository ist erreichbar. Eine SSH-Adresse braucht `GIT_DEPLOY_KEY_FILE` (dann prüft erst der Clone auf dem Server den Zugriff) oder, für GitHub, `VIPER_GH_TOKEN` (dann wird über HTTPS geklont); sonst lehnt der Preflight sie ab.

#### Ergebnis

`Preflight OK.` oder eine Liste der Probleme. Es wurde nichts angelegt oder geändert.

#### Hinweise

* Schreibrechte der Tokens (Hetzner: Server anlegen, Cloudflare: DNS bearbeiten) lassen sich nur lesend nicht prüfen. Fehlen sie, scheitert erst `start.sh`.
* Fehlen in `.env.local` die Werte `SERVER_TYPE`, `SERVER_LOCATION`, `SERVER_IMAGE` oder `SSH_KEY`, gelten die Standardwerte aus Kapitel 4.
* Zeilen mit `WARN` blockieren nicht. Der Preflight warnt etwa, wenn ein Servertyp an einem Standort abgekündigt ist oder `.env.local` für andere Benutzer lesbar ist (dann empfiehlt er `chmod 600 .env.local`, weil die Datei Tokens enthalten kann).

### Erster Testlauf mit einem kleinen Server

Bevor Sie mit einem großen Server arbeiten, prüfen Sie die Einrichtung einmal mit dem kleinsten Typ. Ein Testlauf von etwa einer Stunde kostet nur Cent-Beträge, weil Hetzner stundenweise abrechnet.

#### Voraussetzung

Die erstmalige Einrichtung ist abgeschlossen. Am besten nutzen Sie ein eigenes Hetzner-Projekt nur für diese Rechner. Ein API-Token gilt immer für das ganze Projekt.

#### Vorgehen

1. Tragen Sie in die Einstellungsdatei des Testrechners `machines/test.env.local` die Zeile `SERVER_TYPE=cpx02` ein. Gibt es die Datei noch nicht, kopieren Sie vorher `machines/example.env.local.example` dorthin. Dort können Sie auch `REPO_URL` setzen, dann brauchen Sie die URL in den folgenden Befehlen nicht. `cpx02` hat 1 vCPU, 1 GB RAM und 20 GB Platte.
2. Prüfen Sie vorab: `make preflight MACHINE=test` (mit URL: `make preflight MACHINE=test REPO=https://github.com/<konto>/<repo>.git`). Meldet die Prüfung, dass `cpx02` in `fsn1` derzeit nicht bestellbar ist, ergänzen Sie in derselben Datei `SERVER_LOCATION=nbg1` oder `hel1`, oder nehmen Sie `SERVER_TYPE=cpx12` (2 GB RAM).
3. Starten Sie den Rechner: `make up MACHINE=test` (oder mit `REPO=…` wie oben).
4. Prüfen Sie den Rechner: `make check MACHINE=test` (siehe „Einen laufenden Rechner prüfen“).
5. Löschen Sie den Rechner wieder: `make down MACHINE=test` und bestätigen Sie mit `DELETE`.

Ohne Container lauten die Befehle `./preflight.sh test`, `./start.sh test`, `./check.sh test` und `./stop.sh test`.

#### Ergebnis

Sie wissen, dass Tokens, DNS, SSH-Vertrauen, Einrichtung, Clone und Löschen in Ihrem Konto funktionieren. In der Hetzner Console und bei Cloudflare ist danach nichts mehr vom Testrechner übrig. SSH-Schlüssel und Firewall bleiben für die nächsten Rechner bestehen.

#### Hinweise

* 1 GB RAM reicht für den Funktionstest. Für echte Arbeit des Agenten mit Builds und Tests ist der Speicher zu knapp. Nutzen Sie dafür einen größeren Typ.
* Bricht `make up` ab, bleibt der Server bestehen. Führen Sie `make up MACHINE=test` nach dem Beheben erneut aus, oder löschen Sie ihn mit `make down MACHINE=test`. Ein Server gilt erst nach einem vollständig gelaufenen `make up` als eingerichtet (vermerkt lokal und auf dem Server, gilt also auch für andere Computer); bis dahin wartet ein erneuter Lauf wie bei einem neuen Server, auch auf den Neustart nach den Updates, aber höchstens etwa drei Minuten.

### Einen Rechner für ein Repository einrichten

#### Voraussetzung

Die erstmalige Einrichtung ist abgeschlossen und die drei Zugangsdaten sind gesetzt.

#### Vorgehen

1. Starten Sie den Rechner und geben Sie die Repository-URL mit an:
   ```bash
   ./start.sh web https://git.example.org/team/web.git
   ```
   Ohne URL wird `REPO_URL` aus `machines/web.env.local` verwendet (siehe Kapitel 4).
2. Warten Sie. Das Skript legt Server, Firewall und DNS-Eintrag an und wartet auf den Abschluss der Servereinrichtung. Das dauert meist einige Minuten, höchstens etwa 20. Dabei werden alle Ubuntu-Updates eingespielt. Brauchen sie einen Neustart (etwa ein neuer Kernel), startet der Server einmal neu, und `start.sh` wartet darauf. Nach der Ersteinrichtung schaltet sich cloud-init ab (`/etc/cloud/cloud-init.disabled`), spätere Neustarts laufen ohne cloud-init; `cloud-init status` zeigt dann `disabled`.
3. VS Code öffnet sich automatisch mit dem geklonten Repository, sofern der Befehl `code` vorhanden ist. Sonst öffnen Sie die Verbindung `hetzner-claude-web` in VS Code Remote-SSH selbst.

#### Ergebnis

Die Meldung `Ready: ssh hetzner-claude-web (work dir: …)` erscheint. Das Repository liegt im Arbeitsverzeichnis unter dem Arbeitsverzeichnis (Standard `/Development`).

#### Hinweise

* Ein schon vorhandenes Repository wird beim erneuten Aufruf von `start.sh` für einen laufenden Server nicht erneut geklont.
* Haben Sie das Repository nur beim Start angegeben (`REPO=…`), merkt sich das Skript es für diesen Rechner: Ein späteres `make up` oder `make check` ohne `REPO=` arbeitet mit demselben Repository. Mit `make down` wird es vergessen.
* Das Host-Zertifikat des Servers ist sieben Tage gültig. Bei jedem neuen Server wird ein frisches Zertifikat erstellt. Eine manuelle Prüfung des Fingerabdrucks ist nicht nötig.
* Zusätzlich hinterlegt `start.sh` den Host-Schlüssel des Servers in `~/.ssh/known_hosts_hetzner_<SERVER_PREFIX>-<rechner>`. Die Skripte, `ssh` und VS Code vertrauen auf diesem Computer nur diesem Schlüssel. Ein Schlüssel eines früheren Servers wird abgelehnt, auch wenn sein Zertifikat noch gültig ist. Die Verbindung funktioniert auch nach Ablauf des Zertifikats. Fehlt der hinterlegte Schlüssel (Server auf einem anderen Computer angelegt), gilt bei der ersten Verbindung die Host-CA. Danach trägt das Skript den über diese Verbindung gelesenen Schlüssel als hinterlegten Schlüssel ein. Wird der Rechner neu angelegt (neue Server-ID), verwirft es einen alten hinterlegten Schlüssel.
* Schlägt die Installation von Claude Code bei der Einrichtung fehl, holt `start.sh` sie beim nächsten Aufruf nach.
* Die Skripte verbinden sich über die IP-Adresse des Servers. Den DNS-Eintrag brauchen nur Sie für andere Geräte (etwa das iPad). `start.sh` wartet deshalb nicht darauf, dass er weltweit sichtbar ist.

### Rechner im Container einrichten und beenden

Statt `hcloud`, `jq` und die übrigen Werkzeuge lokal zu installieren, können Sie die Skripte in einem Docker-Container ausführen. Sie brauchen dann nur Docker und `make`.

#### Voraussetzung

* `.env.local` ist angelegt (siehe erstmalige Einrichtung).
* Die drei Zugangsdaten (`HCLOUD_TOKEN`, `CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_ZONE_ID`) stehen in `.env.local` oder sind in der Shell exportiert. Der Container liest beides.

#### Vorgehen

1. Einmalig: `make setup`. Eine Übersicht aller Befehle zeigt `make` (ohne Ziel) bzw. `make help`.
2. Vorab prüfen: `make preflight MACHINE=web`
3. Rechner starten: `make up MACHINE=web REPO=https://git.example.org/team/web.git`
4. Verbinden: `make ssh MACHINE=web` (Shell) oder `make ssh MACHINE=web CMD="<befehl>"` (ein Befehl)
5. Rechner beenden: `make down MACHINE=web` (bei nicht erreichbarem Server `make down MACHINE=web FORCE=1`)

#### Ergebnis

Das Ergebnis ist dasselbe wie beim direkten Aufruf. Login-Schlüssel, Host-CA und SSH-Konfiguration liegen in Ihrem `~/.ssh`, weil der Container dieses Verzeichnis unter demselben Pfad einbindet. Ist auf Ihrem Rechner ein OpenSSH-Client installiert, funktionieren daher auch `ssh hetzner-claude-web` und VS Code Remote-SSH direkt vom Host aus. Ohne lokalen Client nutzen Sie `make ssh`.

#### Hinweise

* VS Code wird aus dem Container nicht gestartet. Öffnen Sie die Verbindung `hetzner-claude-web` selbst.
* `make up` baut das Image bei Bedarf und übernimmt Ihre Benutzerkennung und Ihr Home-Verzeichnis. Das Image gilt daher nur für Ihren Benutzer.
* Der Container sieht das Repository nur lesend.
* Von Ihrem Home-Verzeichnis sieht der Container nur `~/.ssh`. Legen Sie Dateien für `GIT_DEPLOY_KEY_FILE` und `IPAD_SSH_PUBLIC_KEY_FILE` deshalb dort ab. Sonst bricht `start.sh` mit „File not readable“ ab, bevor etwas angelegt wird.
* Die Skripte nutzen nur die SSH-Konfiguration des jeweiligen Rechners (`~/.ssh/config_hetzner_<SERVER_PREFIX>-<rechner>`). Einstellungen in Ihrer `~/.ssh/config` stören daher nicht.
* Ist `~/.ssh/config` ein Symlink, etwa in ein Dotfiles-Repository, bindet `make` dessen Ziel zusätzlich unter demselben Pfad ein, damit die `Include`-Zeile eingetragen werden kann. Lässt sich die Datei trotzdem nicht bearbeiten, warnt der Preflight, und `make up` läuft weiter. Nur `ssh <alias>` vom Host und VS Code brauchen die Zeile.

### Einen laufenden Rechner prüfen

#### Vorgehen

Führen Sie `make check MACHINE=<rechner>` aus (ohne Container: `./check.sh <rechner>`). Jede Zeile beginnt mit `ok`, `info`, `WARN` oder `FAIL`.

Geprüft werden:

* **DNS:** Der Name `<SERVER_PREFIX>-<rechner>.<DNS_DOMAIN>` löst bei den öffentlichen Resolvern von Cloudflare (1.1.1.1) und Google (8.8.8.8) auf die IP-Adresse des Servers auf. Zusätzlich zeigt der Check, was Ihr Computer auflöst.
* **Zugriff wie ein anderes Gerät:** eine SSH-Verbindung über den DNS-Namen, bei der nur der Host-CA vertraut wird, nicht dem hinterlegten Schlüssel. So sieht das iPad oder ein zweiter Computer den Server, und dabei wird geprüft, dass das Zertifikat für den Namen gilt. Der Check zeigt auch, bis wann das Zertifikat gültig ist.
* **Server:** cloud-init ist fertig. Claude Code ist installiert und, wenn `VIPER_CLAUDE_TOKEN` gesetzt ist, angemeldet und antwortet. `gh` ist installiert und, wenn `VIPER_GH_TOKEN` gesetzt ist, angemeldet. Docker läuft, Homebrew ist installiert und im `PATH` (wenn `VIPER_HOMEBREW=1`), das Repository ist geklont, und die SSH-Härtung wirkt. Dazu kommen freier Speicher und Plattenplatz.

#### Ergebnis

`Check OK.` oder eine Liste der Probleme. Der Check ändert nichts.

#### Hinweise

* Mit gesetztem `VIPER_CLAUDE_TOKEN` oder `VIPER_ANTHROPIC_BASE_URL` stellt der Check Claude Code eine kurze Testanfrage. Sie zählt zu Ihrem Abo bzw. wird beim anderen Anbieter abgerechnet.
* Fehler der Ersteinrichtung liest der Check aus `/var/lib/cloud/data/result.json`, auch wenn cloud-init danach abgeschaltet ist.
* Haben spätere Updates auf dem Server einen Neustart nötig gemacht (Ubuntu meldet beim Login `*** System restart required ***`), warnt der Check. Starten Sie den Server dann mit `make ssh MACHINE=<rechner> CMD="sudo reboot"` neu. Laufende tmux-Sitzungen enden dabei, Dateien bleiben erhalten.
* Direkt nach dem Anlegen kann die DNS-Prüfung für einige Minuten fehlschlagen, bis die Resolver den neuen Eintrag kennen.
* Der lokale Resolver läuft bei `make check` im Container, nicht auf Ihrem Computer. Was Ihr Computer auflöst, prüfen Sie mit `getent hosts <name>` oder `ping <name>`.

### Protokolle ansehen

`start.sh` (`make up`), `stop.sh` (`make down`), `check.sh` (`make check`) und ein eigenständig aufgerufenes `preflight.sh` schreiben zwei Protokolle in `~/.local/state/hetzner-claude/` (Rechte `600`, ohne Tokens):

* `events.log`: eine Zeile pro Ereignis mit Zeit (lokale Zeit mit Abstand zu UTC, etwa `2026-10-21T15:12:03+02:00`), Server, Aktion und Ergebnis, etwa `server created id=…`, `ready`, `deleted`, `cancelled (unsaved work)`, `interrupted` (Strg-C, geschlossenes Terminal) oder `FAILED (exit 1; …)`. Daraus sehen Sie, welche Server wann liefen.
* `logs/<SERVER_PREFIX>-<rechner>.log`: die vollständige Ausgabe jedes Laufs, Läufe getrennt durch eine Zeile `===== <Zeit> <aktion> <server>`. Am Ende jedes `make up` steht dort unter `== installed on the server ==`, was auf dem Server installiert ist: Betriebssystem und Kernel, die apt-Pakete aus `cloud-init.yaml` mit Version, Claude Code, `gh`, Docker sowie Homebrew mit Taps, vertrauten Einträgen und den angeforderten Formeln samt Version. Das Protokoll von Homebrew selbst (`~/.config/viper/brew-packages.log`) bleibt auf dem Server.

#### Vorgehen

* Alle Ereignisse (die letzten 50): `make log`
* Ereignisse eines Rechners: `make log MACHINE=<rechner>`
* Vollständige Ausgabe eines Rechners: `make log MACHINE=<rechner> FULL=1`

`make log` läuft direkt auf Ihrem Rechner, ohne Container und ohne Tokens. `FULL` wirkt nur mit dem Wert `1`, `FORCE` (bei `make down`) mit `1`, `yes` oder `true`. Eine Repo-URL mit Zugangsdaten (`https://user:token@…`) lehnt der Preflight ab, damit das Token weder in die Protokolle noch auf den Server gelangt. Die Protokolle werden nicht automatisch gekürzt; löschen Sie sie bei Bedarf von Hand. Protokolle auf dem Server (cloud-init, Homebrew) verschwinden dagegen mit dem Server.

### Mit Claude Code arbeiten

#### Vorgehen

1. Öffnen Sie in VS Code das integrierte Terminal, oder verbinden Sie sich mit `make ssh MACHINE=<rechner>`.
2. Wechseln Sie in das Repository-Verzeichnis.
3. Starten Sie eine dauerhafte Sitzung und Claude Code:
   ```bash
   tmux new-session -A -s claude
   claude
   ```
4. Zum Verlassen der Sitzung drücken Sie **Strg+B**, dann **D**. Die Sitzung läuft weiter.
5. Verbinden Sie sich später erneut, etwa mit `make ssh MACHINE=<rechner> CMD="tmux attach -t claude"` (bei einem Befehl im Terminal fordert `make ssh` ein Terminal an, das tmux braucht) oder von einem beliebigen SSH-Client mit `tmux attach -t claude`.

#### Ergebnis

Claude Code läuft in einer Sitzung, die einen Verbindungsabbruch übersteht.

#### Hinweise

* Neue Server bekommen eine `~/.tmux.conf` für `builder` (Maus an, Fenster ab 1 nummeriert, `Esc` ohne Verzögerung, großer Verlauf). Mit **Alt+1** bis **Alt+9** wechseln Sie direkt zwischen Fenstern; sonst gelten die üblichen Kürzel (**Strg+B**, dann **N**/**P** oder eine Ziffer, **C** für ein neues Fenster). Mit eingeschalteter Maus markieren Sie Text zum Kopieren mit gedrückter **Umschalttaste**. Auf einem bestehenden Server wirkt die Datei nicht; legen Sie sie dort von Hand an. Eigene Änderungen gehen mit dem Server verloren.
* Homebrew (`brew`) wird beim ersten Einrichten mit dem offiziellen Installationsskript nach `/home/linuxbrew/.linuxbrew` installiert und ist in allen Shells verfügbar, auch für Claude Code. Das kostet ein bis zwei Minuten. Mit `VIPER_HOMEBREW=0` (etwa in `machines/<rechner>.env.local`) entfällt es. Scheitert die Installation, warnt `start.sh` nur; ein erneuter `make up` versucht es wieder.
* Interaktive Shells starten im Arbeitsverzeichnis des Rechners (beim geklonten Repository), wenn sie im Home-Verzeichnis beginnen würden. Das gilt für `make ssh`, tmux und einen normalen SSH-Login. VS-Code-Terminals öffnen ohnehin im Projektordner, und einzelne SSH-Befehle sind nicht betroffen.
* Ohne `VIPER_CLAUDE_TOKEN` müssen Sie das Anmelden bei Claude nach jedem neuen Server wiederholen, weil `/home/builder/.claude` mit dem Server gelöscht wird. Mit Token entfällt das (siehe „Claude Code ohne erneute Anmeldung nutzen“).

### Mehrere Rechner gleichzeitig betreiben

#### Vorgehen

Starten Sie für jedes Repository einen Rechner mit eigenem Namen:

```bash
./start.sh web https://git.example.org/team/web.git
./start.sh api https://git.example.org/team/api.git
```

Jeder Rechner hat einen eigenen Server, eine eigene Platte und einen eigenen DNS-Namen. Sie beenden sie getrennt mit `./stop.sh web` und `./stop.sh api`.

### Ein privates Repository per SSH klonen

#### Voraussetzung

Sie haben im Git-Dienst einen **Deploy-Key** (nur lesend) für das Repository hinterlegt. Die Datei mit dem privaten Schlüssel liegt auf Ihrem lokalen Rechner.

#### Vorgehen

1. Kopieren Sie `machines/example.env.local.example` nach `machines/<rechner>.env.local`.
2. Tragen Sie `REPO_URL` (SSH-Adresse) und `GIT_DEPLOY_KEY_FILE` (Pfad zur Schlüsseldatei) ein.
3. Starten Sie den Rechner mit `make up MACHINE=<rechner>`.

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
3. Prüfen Sie vorab: `make preflight MACHINE=<rechner> REPO=<url>`. Unter `== GitHub` stehen dann `VIPER_GH_TOKEN valid for …`, `token can access <konto>/<repo>` und `token can push to <konto>/<repo>`.
4. Starten Sie den Rechner mit `./start.sh <rechner>` bzw. `make up MACHINE=<rechner>`.

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

* Die Skripte lesen bewusst nicht `GH_TOKEN`. Ein Token, das Sie für Ihr lokales `gh` exportiert haben, landet so nie ungewollt auf einem Server.
* Mit **Contents: Read and write** darf der Agent auf jeden Branch pushen, auch direkt auf `main`, und er darf Force-Pushes ausführen und Branches löschen. Weitere Einstellungen sind dafür nicht nötig. Soll er nur über Pull Requests arbeiten, schützen Sie `main` im Repository unter **Settings → Rules → Rulesets**, etwa mit „Require a pull request before merging“, „Block force pushes“ und „Restrict deletions“. Bei privaten Repositories gibt es Rulesets erst ab GitHub Pro bzw. Team.
* Der Preflight prüft das Schreibrecht, ohne etwas zu schreiben: Er ruft die Liste der Referenzen ab, mit der jeder `git push` beginnt. Das Recht **Workflows** und Regeln für einzelne Branches prüft er nicht.
* Das Token wird per SSH über die Standardeingabe übertragen. Es steht weder in den Hetzner-User-Data noch in einer Befehlszeile.
* Auf dem Server liegt das Token unverschlüsselt in `~/.config/gh/hosts.yml`. Claude Code und jeder Prozess des Benutzers `builder` können es lesen. Vergeben Sie deshalb nur die Rechte, die der Agent braucht.
* Mit dem Löschen des Servers ist das Token dort entfernt. Bei GitHub bleibt es gültig, bis es abläuft oder Sie es widerrufen.

### Claude Code ohne erneute Anmeldung nutzen

Ohne weitere Einstellung melden Sie sich nach jedem neuen Server auf dem Rechner bei Claude an (`claude`, dann `/login`). Mit einem langlebigen Token Ihres Claude-Abos (Pro, Max, Team oder Enterprise) erledigt `start.sh` das für Sie. Die Nutzung auf dem Server zählt dann zu Ihrem Abo, nicht zu API-Guthaben.

#### Voraussetzung

Sie haben ein Claude-Abo und auf einem Rechner mit Browser Claude Code installiert.

#### Vorgehen

1. Erzeugen Sie das Token einmalig in **Ihrem eigenen Terminal**: `claude setup-token`. Folgen Sie der Anmeldung im Browser und kopieren Sie das angezeigte Token.
2. Tragen Sie es als `VIPER_CLAUDE_TOKEN=…` ein, für einen Rechner in `machines/<rechner>.env.local` oder für alle in `.env.local`. Achten Sie darauf, dass die Datei die Rechte `600` hat.
3. Starten Sie den Rechner mit `make up MACHINE=<rechner>`. Bei einem laufenden Rechner reicht derselbe Befehl, er überträgt das Token neu.

#### Ergebnis

Die Meldung `claude: logged in with VIPER_CLAUDE_TOKEN` erscheint. `claude` ist auf dem Server in SSH-Befehlen, in tmux und im VS-Code-Terminal angemeldet. In Claude Code zeigt `/status` im Tab **Status** dann `Auth token: CLAUDE_CODE_OAUTH_TOKEN`. Prüfen können Sie das mit `make check MACHINE=<rechner>` oder direkt mit `make ssh MACHINE=<rechner> CMD="bash -lc 'claude -p \"Antworte nur mit OK\" </dev/null'"`. Das `</dev/null` ist nötig, weil `claude -p` sonst auf weitere Eingaben wartet.

#### Hinweise

* Das Token wird per SSH über die Standardeingabe übertragen. Es steht weder in den Hetzner-User-Data noch in einer Befehlszeile. Auf dem Server liegt es in `~/.config/viper/claude.env` (Rechte `600`), geladen von einer Zeile am Anfang von `~/.bashrc` und `~/.profile`.
* Claude Code und jeder Prozess des Benutzers `builder` können das Token lesen. Es gilt ein Jahr und gibt Zugriff auf Ihr Abo. Behandeln Sie es wie ein Passwort und geben Sie es nicht in Chats oder Tickets weiter.
* Die Skripte lesen bewusst nicht `CLAUDE_CODE_OAUTH_TOKEN`. Ein lokal gesetztes Token landet so nicht ungewollt auf einem Server.
* Das Token kann nur Modell-Anfragen stellen. Remote-Control-Sitzungen und claude.ai-Connectors gehen damit nicht, lokal konfigurierte MCP-Server schon. `claude --bare` liest das Token nicht.
* Setzen Sie auf dem Server kein `ANTHROPIC_API_KEY`: Ein API-Key hat Vorrang vor dem Token und würde über API-Guthaben abrechnen.
* Ist das Token abgelaufen, erzeugen Sie mit `claude setup-token` ein neues, tragen es ein und führen `make up MACHINE=<rechner>` erneut aus.

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
3. Prüfen Sie vorab: `make preflight MACHINE=<rechner>`. Der Preflight stellt dem Modell eine kurze Anfrage, genau wie Claude Code sie stellt, und meldet einen falschen Schlüssel oder Modellnamen.
4. Starten Sie den Rechner: `make up MACHINE=<rechner>`. Bei einem laufenden Rechner reicht derselbe Befehl.

#### Ergebnis

Die Meldung `claude: using model <Modell> at <Adresse>` erscheint. `claude` nutzt auf dem Server dieses Modell, auch für Hintergrundaufgaben und Subagents. `make check MACHINE=<rechner>` zeigt `model <Modell> at <Adresse> answers`.

#### Hinweise

* **Modellnamen:** Über `https://ollama.com` heißen Cloud-Modelle ohne das Suffix `:cloud`, das ein lokales Ollama verwendet. Welcher Name stimmt, zeigt der Preflight. Ein Suffix wie `[1m]` ist eine Markierung von Claude Code und gehört nicht zum Modellnamen; für Fremdmodelle setzen Sie das Fenster besser mit `VIPER_CLAUDE_MAX_CONTEXT_TOKENS`.
* **Abrechnung** erfolgt beim Anbieter des Modells, nicht über Ihr Claude-Abo.
* **Einschränkungen:** Das Modell muss Tools (Funktionsaufrufe) beherrschen. Laut Ollama funktionieren gehostete Websuche und manche erweiterten Tool-Funktionen nicht vollständig. Remote Control ist mit einer anderen Adresse als `api.anthropic.com` abgeschaltet. Qualität und Verhalten bei langen Aufgaben hängen stark vom Modell ab.
* Der Schlüssel wird wie das Abo-Token per SSH über die Standardeingabe übertragen und liegt auf dem Server in `~/.config/viper/claude.env` (Rechte `600`). Claude Code und jeder Prozess von `builder` können ihn lesen.
* Wechseln Sie zurück zum Abo, entfernen Sie die drei Einstellungen, setzen Sie `VIPER_CLAUDE_TOKEN` und führen Sie `make up MACHINE=<rechner>` erneut aus.

### Werkzeuge mit Homebrew installieren

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
   * `VIPER_BREW_TRUST`: `owner/repo` vertraut dem ganzen Tap, `owner/repo/name` einer einzelnen Formel. Mit `cask:` oder `command:` davor vertrauen Sie einem Cask oder einem externen Befehl. Meist ist das nicht nötig: Wer `owner/repo/name` installiert, vertraut damit genau dieser Formel. Vertrauen Sie so wenig wie möglich, also lieber einer Formel als einem ganzen Tap. `brew trust` nimmt auch Tippfehler an; `make check` meldet einen vertrauten, aber nicht eingebundenen Tap als `untapped:…`.
   * `VIPER_BREW_PACKAGES`: `name` aus dem Hauptkatalog oder `owner/repo/name` aus einem Tap.
2. Prüfen Sie vorab mit `make preflight MACHINE=<rechner>`. Er meldet Einträge im falschen Format.
3. Starten Sie den Rechner mit `make up MACHINE=<rechner>`. `start.sh` führt die Schritte in der Reihenfolge Tap, Trust, Installation aus und meldet jeden Schritt (`brew: tap …`, `brew: trusted …`, `brew: installed …`).

#### Vorgehen mit einer Brewfile im Repository

Eine `Brewfile` wird nur angewendet, wenn Sie `VIPER_BREWFILE=1` setzen. Sie ist Ruby-Code aus dem Repository, den `brew bundle` ausführt; ihre `trusted:`-Angaben bleiben dauerhaft in Homebrews Trust-Speicher, und sie kann auch andere Paketarten installieren. Setzen Sie die Option deshalb nur für Repositories, deren Inhalt Sie vertrauen. Der Preflight weist mit `WARN` darauf hin.

Legen Sie im Repository eine Datei `Brewfile` an. Homebrew unterscheidet dort selbst zwischen Taps und Formeln, und der Trust steht direkt am Tap:
```ruby
tap "hashicorp/tap", trusted: true
brew "ripgrep"
brew "hashicorp/tap/terraform"
```
Statt dem ganzen Tap können Sie einzelnen Formeln vertrauen: `tap "hashicorp/tap", trusted: { formulae: ["terraform"] }`. Mit `VIPER_BREWFILE=1` führt `start.sh` nach dem Klonen `brew bundle install --no-upgrade` mit dieser Datei aus, und `make check` prüft sie mit `brew bundle check`. So bekommt jeder Rechner dieses Projekts dieselben Werkzeuge, versioniert mit dem Code.

#### Ergebnis

Die Werkzeuge stehen in allen Shells zur Verfügung, auch für Claude Code. `make check` meldet `Homebrew: taps, trust entries and formulae present` bzw. `Brewfile of the repository satisfied`, oder was fehlt.

#### Hinweise

* Schritte, die scheitern, brechen die Einrichtung nicht ab. `start.sh` warnt, das Protokoll liegt auf dem Server in `~/.config/viper/brew-packages.log`, und ein erneuter `make up` versucht es wieder.
* Jedes Paket verlängert die Einrichtung. Manche Formeln baut Homebrew aus dem Quellcode, das dauert auf kleinen Servern spürbar.
* Homebrew wird dabei nicht selbst aktualisiert, und vorhandene Pakete werden nicht aktualisiert (`--no-upgrade`).
* Vertrauen Sie nur Taps, deren Herausgeber Sie vertrauen: Formeln sind Ruby-Code, den Homebrew beim Installieren ausführt.
* Von Hand geht es jederzeit auch: `make ssh MACHINE=<rechner> CMD="bash -lc 'brew install <name>'"`.

### Vom iPad aus verbinden

#### Vorgehen

1. Erzeugen Sie auf dem iPad ein SSH-Schlüsselpaar und speichern Sie den öffentlichen Schlüssel als Datei auf Ihrem lokalen Rechner.
2. Tragen Sie den Pfad bei `IPAD_SSH_PUBLIC_KEY_FILE` in `.env.local` ein.
3. Starten Sie einen neuen Rechner. Der Schlüssel wird für den Benutzer `builder` hinterlegt.
4. Verbinden Sie sich mit Benutzer `builder` und dem DNS-Namen des Rechners. `IPAD_SSH_PUBLIC_KEY_FILE` muss der **öffentliche** Schlüssel sein (`.pub`); einen privaten lehnt der Preflight ab, weil die Datei in die User-Data des Servers kommt.

#### Hinweise

* Bei einem bereits laufenden Server tragen Sie den Schlüssel selbst in `/home/builder/.ssh/authorized_keys` ein.
* Die Prüfung des Server-Zertifikats funktioniert nur, wenn die iPad-App Host-Zertifikate unterstützt. Die öffentliche Host-CA gibt `setup.sh` am Ende aus.
* Erlaubt die Firewall Ihr Netz nicht, kommen Sie nicht durch (siehe Kapitel 4, Firewall).

### Einen Rechner beenden

#### Voraussetzung

Schließen Sie alle VS-Code-Fenster und Terminals, die auf dem Rechner geöffnet sind, und beenden Sie die tmux-Sitzungen.

#### Vorgehen

1. Führen Sie `./stop.sh <rechner>` aus.
2. Lesen Sie die Ausgabe: Das Skript zeigt je Repository und je Worktree den Git-Stand, Commits, die auf keinem Remote liegen (auf allen lokalen Branches), und die tmux-Sitzungen. Es sucht Repositories in allen Arbeitsverzeichnissen, mit denen der Rechner je gestartet wurde (auch wenn Sie `WORKSPACE_DIR` später geändert haben oder den Rechner von einem anderen Computer aus gestartet haben; der Server führt die Liste selbst mit), im Home-Verzeichnis von `builder` und in `/tmp` und `/var/tmp`. Dateien und Ordner direkt im Arbeitsverzeichnis, die zu keinem Repository gehören, listet es unter **Not in any git repository (lost)** auf. Dateien außerhalb von Repositories im Home-Verzeichnis oder in `/tmp` prüft es nicht. Neue Dateien findet es unabhängig von Git-Einstellungen wie `status.showUntrackedFiles=no`; Dateien, die per `assume-unchanged` oder `skip-worktree` aus `git status` ausgeblendet sind, meldet es eigens, ebenso Commits, die nur über einen Tag erreichbar sind, der auf dem Server neu angelegt wurde (Tags, die mit dem Clone kamen oder auf einem Remote liegen, zählen nicht). Bricht die Prüfung auf dem Server vorzeitig ab, löscht es nichts. Liegt ein Remote eines Repositorys auf dem Server selbst (etwa ein lokales Bare-Repository), meldet es auch das, weil dorthin gepushte Commits mit dem Server verloren gehen. Gibt es ungesicherte Arbeit (uncommittete Änderungen, Commits auf keinem Remote, mit `git stash` beiseitegelegte Änderungen, Dateien außerhalb eines Repositorys, ein fehlendes Arbeitsverzeichnis oder ein nicht lesbares Repository), steht `UNSAVED WORK` in der Ausgabe, und Sie müssen zusätzlich `DISCARD` eingeben, um fortzufahren. Von Git ignorierte Dateien (zum Beispiel `.env` oder Build-Verzeichnisse) werden unter **Ignored by git (also lost)** aufgelistet, blockieren aber nicht.
3. Sichern Sie offene Änderungen (committen und pushen), falls nötig, und starten Sie `stop.sh` erneut.
4. Geben Sie zur Bestätigung `DELETE` ein.

#### Ergebnis

Server, DNS-Eintrag und die lokale SSH-Konfiguration des Rechners sind gelöscht. Alle Dateien auf dem Server sind weg. Beim nächsten `./start.sh <rechner>` wird das Repository neu geklont.

#### Hinweise

* Auch Ihre Claude-Anmeldung und alle nicht gepushten Änderungen gehen verloren.
* Das Skript bricht ab, wenn Sie `DELETE` nicht eingeben.
* Findet `stop.sh` den Server im Hetzner-Projekt nicht (etwa weil Sie ihn in der Hetzner Console gelöscht haben), räumt es SSH-Konfiguration, hinterlegten Schlüssel und DNS-Eintrag erst nach Eingabe von `CLEANUP` auf. So richtet ein Token für das falsche Projekt keinen Schaden an. Den DNS-Eintrag löscht es nur, wenn er noch auf die zuletzt bekannte IP-Adresse zeigt.

### Einen nicht erreichbaren Rechner löschen

#### Vorgehen

Führen Sie `./stop.sh <rechner> --force` aus und bestätigen Sie mit `DELETE`.

#### Hinweise

Mit `--force` entfällt die Git-Prüfung. Nicht gepushte Änderungen gehen verloren. Nutzen Sie das nur, wenn SSH nicht erreichbar ist.

## 4. Einstellungen

Die Einstellungen stehen in `.env.local`. Für einzelne Rechner können Sie sie in `machines/<rechner>.env.local` überschreiben.

| Einstellung | Bedeutung | Standard |
|---|---|---|
| `SERVER_PREFIX` | Namensanfang aller Ressourcen | `claude` |
| `SERVER_TYPE` | Hetzner-Servertyp | `cpx32` |
| `SERVER_LOCATION` | Standort | `fsn1` |
| `SERVER_IMAGE` | Betriebssystem-Image | `ubuntu-26.04` |
| `DNS_DOMAIN` | Ihre Domain, Pflichtangabe | leer |
| `SSH_KEY` | Pfad Ihres Login-Schlüssels | `~/.ssh/hetzner-build` |
| `SSH_ALLOWED_CIDRS` | Netze, die Port 22 erreichen dürfen | `0.0.0.0/0,::/0` |
| `IPAD_SSH_PUBLIC_KEY_FILE` | Öffentlicher Schlüssel des iPads | leer |
| `VIPER_HOMEBREW` | Homebrew (`brew`) auf dem Server installieren: `1` oder `0` | `1` |
| `VIPER_BREW_TAPS` | Homebrew-Taps (`owner/repo` oder `owner/repo=URL`), mit Leerzeichen getrennt | leer |
| `VIPER_BREW_TRUST` | Einträge für `brew trust`: `owner/repo` (Tap), `owner/repo/name` (Formel), `cask:…`, `command:…` | leer |
| `VIPER_BREW_PACKAGES` | Zu installierende Formeln (`name` oder `owner/repo/name`) | leer |
| `VIPER_BREWFILE` | `1`: eine `Brewfile` im geklonten Repository mit `brew bundle` anwenden | `0` |
| `WORKSPACE_DIR` | Verzeichnis auf dem Server, in das die Repositories geklont werden | `/Development` |
| `REPO_URL` | Repository, das beim Start geklont wird | leer |
| `REPO_BRANCH` | Zweig, der geklont wird | Standardzweig |
| `GIT_DEPLOY_KEY_FILE` | Deploy-Key für private SSH-Repositories | leer |
| `VIPER_GH_TOKEN` | GitHub-PAT für `gh` und `git` über HTTPS auf dem Server | leer |
| `VIPER_GH_HOST` | GitHub-Host (für GitHub Enterprise) | `github.com` |
| `VIPER_CLAUDE_TOKEN` | Langlebiges Token Ihres Claude-Abos aus `claude setup-token` (ein Jahr gültig) | leer |
| `VIPER_ANTHROPIC_BASE_URL` | Adresse einer Anthropic-kompatiblen Schnittstelle für ein anderes Modell (z. B. `https://ollama.com`) | leer |
| `VIPER_ANTHROPIC_AUTH_TOKEN` | API-Schlüssel für diese Schnittstelle | leer |
| `VIPER_CLAUDE_MODEL` | Modellname bei diesem Anbieter | leer |
| `VIPER_CLAUDE_MAX_CONTEXT_TOKENS` | Kontextfenster, das Claude Code für das Modell annimmt | 200.000 |
| `HCLOUD_TOKEN`, `CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_ZONE_ID` | Zugangsdaten (Shell oder `.env.local`), Pflichtangaben | leer |

### Firewall und Anmeldung

* Die Firewall erlaubt eingehend SSH (TCP-Port 22) und ICMP (`ping`), beides aus den Netzen in `SSH_ALLOWED_CIDRS`. Fehlt eine der beiden Regeln, ergänzt `start.sh` sie, auch in einer schon vorhandenen Firewall. Andere Regeln lässt es unverändert. Eine bestehende Regel ändert es nicht: Wollen Sie die Netze nachträglich einschränken, ändern Sie die Regeln in der Hetzner Console.
* Die SSH-Konfiguration jedes Rechners verbietet Agent- und X11-Weiterleitung (`ForwardAgent no`, `ForwardX11 no`), auch wenn Ihre `~/.ssh/config` sie allgemein erlaubt. Der Agent auf dem Server hat Root-Rechte und soll Ihre lokalen SSH-Schlüssel nicht benutzen können.
* Standardmäßig ist SSH aus dem ganzen Internet erreichbar. Anmeldung ist nur mit SSH-Schlüssel möglich. Passwörter und Root-Login sind abgeschaltet. Schränken Sie die Netze ein, wenn Sie nur von festen Adressen arbeiten. Eine Alternative ist ein VPN.
* Ausgehende Verbindungen des Servers sind nicht eingeschränkt.

### DNS

Der DNS-Eintrag ist ein A-Record ohne Cloudflare-Proxy, weil dieser SSH nicht weiterleitet. Das Skript ändert oder löscht nur Einträge, die es selbst angelegt hat (Kommentar `managed by hetzner-claude`).

## 5. Rollen und Rechte

| Rolle | Wo | Rechte |
|---|---|---|
| Sie (lokal) | Ihr Rechner | Führen die Skripte aus, besitzen API-Tokens, Login-Schlüssel und CA-Schlüssel. |
| `builder` | Server | Arbeitsbenutzer mit sudo ohne Passwort und Docker-Zugriff, entspricht Root. |
| `root` | Server | Anmeldung per SSH ist gesperrt. |

## 6. Import und Export

Die Skripte importieren und exportieren keine Daten. Ihre Arbeitsdateien liegen auf der Platte des Servers. Sichern Sie Ergebnisse durch `git push`.

## 7. Fehlerbehebung

### Meldung: "Copy .env.local.example to .env.local"

**Ursache:** Die Datei `.env.local` fehlt.  
**Lösung:** Kopieren Sie `.env.local.example` nach `.env.local` und tragen Sie `DNS_DOMAIN` ein.

### Meldung: "Missing settings: …"

**Ursache:** Pflichtwerte fehlen: `DNS_DOMAIN`, `HCLOUD_TOKEN`, `CLOUDFLARE_API_TOKEN` oder `CLOUDFLARE_ZONE_ID`. Die Meldung nennt alle fehlenden auf einmal. Es wurde nichts angelegt oder gelöscht.  
**Lösung:** Tragen Sie die Werte in `.env.local` oder `machines/<rechner>.env.local` ein. Die drei Tokens (`HCLOUD_TOKEN`, `CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_ZONE_ID`) und alle `VIPER_*`-Einstellungen können Sie auch in der Shell exportieren; andere Einstellungen (etwa `DNS_DOMAIN`, `SERVER_TYPE`) erreichen den `make`-Container nur über die Dateien. Steht eine Einstellung in einer Datei, hat sie Vorrang vor der Shell. Beachten Sie: Eine leere Zeile `HCLOUD_TOKEN=` in `.env.local` überschreibt den Wert aus der Shell.

### Meldung: "DNS_DOMAIN must be a domain like example.com"

**Ursache:** `DNS_DOMAIN` enthält Großbuchstaben, ein Schema wie `https://`, einen Pfad oder einen Punkt am Ende.  
**Lösung:** Tragen Sie nur den Domainnamen ein, zum Beispiel `example.com`.

### Meldung: "Preflight: … problem(s); nothing was created."

**Ursache:** Die Vorabprüfung hat Probleme gefunden. Die Zeilen mit `FAIL` darüber nennen sie. Häufige Fälle:
* `… cannot currently be ordered in …` oder `… is not offered in …`: Der Servertyp ist am Standort ausverkauft oder dort nicht erhältlich. Die Meldung nennt die Standorte, an denen er bestellbar ist. Setzen Sie `SERVER_LOCATION` (etwa `nbg1` oder `hel1`) oder einen anderen `SERVER_TYPE`, am besten in `machines/<rechner>.env.local`.
* `server type … does not exist`, `location … does not exist`, `image … does not exist`: Tippfehler oder ein Wert, den Hetzner nicht anbietet. Die gültigen Werte zeigen `hcloud server-type list`, `hcloud location list` und `hcloud image list --type system`.
* `DNS_DOMAIN … is not in Cloudflare zone …`: `CLOUDFLARE_ZONE_ID` gehört zu einer anderen Domain.
* `repository … not reachable without credentials`: Das Repository ist privat. Setzen Sie `VIPER_GH_TOKEN` (GitHub) oder nutzen Sie eine SSH-URL mit `GIT_DEPLOY_KEY_FILE`.
* `token cannot access …`: Das fein granulare Token gilt nicht für dieses Repository oder gehört zu einem anderen Resource owner. `token cannot push to …`: Das Token darf nur lesen; setzen Sie **Contents** auf „Read and write“ (Abschnitt „GitHub-CLI und Token auf dem Rechner nutzen“). Die Rechte eines vorhandenen Tokens können Sie bei GitHub nachträglich ändern (**Edit**); ein neues Token ist nicht nötig.

**Lösung:** Beheben Sie die genannten Punkte und führen Sie `./preflight.sh <rechner>` erneut aus.

### Meldung: "Machine name required" oder "Machine name: lowercase letters, digits, hyphens"

**Ursache:** Der Rechnername fehlt oder enthält ungültige Zeichen.  
**Lösung:** Geben Sie einen Namen aus Kleinbuchstaben, Ziffern und Bindestrichen an, zum Beispiel `./start.sh web`.

### Meldung: "Run ./setup.sh first"

**Ursache:** Login-Schlüssel oder Host-CA fehlen.  
**Lösung:** Führen Sie `./setup.sh` aus.

### Meldung: "hcloud … list failed (check HCLOUD_TOKEN / project / network)"

**Ursache:** Die Hetzner-API hat nicht geantwortet oder das Token abgelehnt: Es ist abgelaufen, gehört zu einem anderen Projekt oder hat keine Schreibrechte, oder das Netz ist gestört. Die Skripte brechen dann ab, ohne etwas anzulegen, zu löschen oder aufzuräumen.  
**Lösung:** Prüfen Sie `HCLOUD_TOKEN` und das Hetzner-Projekt und führen Sie den Befehl erneut aus.

### Meldung: "SSH not reachable; server retained."

**Ursache:** Der Server ist nach etwa 20 Minuten nicht per SSH erreichbar. Mögliche Gründe: Die Firewall blockiert Ihr Netz, oder der Server startet nicht. (Ein nicht vertrauenswürdiger Host-Schlüssel oder ein abgelehnter Login-Schlüssel hat eigene Meldungen, siehe unten.)  
**Lösung:**
1. Prüfen Sie die Firewall-Regel für Port 22 in der Hetzner Console.
2. Öffnen Sie die Web-Konsole des Servers und lesen Sie `/var/log/cloud-init-output.log`.
3. Starten Sie `./start.sh <rechner>` erneut, wenn die Ursache behoben ist.
4. Hilft das nicht, löschen Sie den Rechner mit `./stop.sh <rechner> --force` und legen Sie ihn neu an.

### Meldung: "SSH: the server rejects the login key"

**Ursache:** Der Server lehnt Ihren Login-Schlüssel ab (`Permission denied`). Meist wurde der Server mit einem anderen Login-Schlüssel angelegt, etwa von einem anderen Computer oder mit einem anderen `SSH_KEY`. Bei einem bereits laufenden Server meldet `start.sh` das sofort, statt lange zu warten.  
**Lösung:** Nutzen Sie den Computer bzw. Schlüssel, mit dem der Rechner angelegt wurde, oder tragen Sie Ihren öffentlichen Schlüssel dort in `/home/builder/.ssh/authorized_keys` ein.

### Meldung: "Claude Code installation failed; server retained."

**Ursache:** Die Installation von Claude Code ist bei der Einrichtung und beim Nachholen gescheitert, meist wegen eines Netzwerkproblems.  
**Lösung:** Führen Sie `./start.sh <rechner>` erneut aus. Das Skript versucht die Installation dann noch einmal.

### Meldung: "Docker is not running"

**Ursache:** Die Servereinrichtung ist unvollständig, Docker läuft nicht.  
**Lösung:** Lesen Sie `/var/log/cloud-init-output.log` auf dem Server. Meist hilft es, den Rechner mit `./stop.sh <rechner>` zu löschen und neu anzulegen.

### Meldung: "WARNING: cloud-init reported errors"

**Ursache:** cloud-init meldet Fehler, aber Claude Code und Docker laufen. Der Rechner ist nutzbar.  
**Lösung:** Prüfen Sie bei Bedarf `/var/log/cloud-init-output.log` auf dem Server.

### Meldung: "SSH key … belongs to a different public key"

**Ursache:** Im Hetzner-Projekt gibt es schon einen SSH-Schlüssel namens `<SERVER_PREFIX>-login`, aber mit einem anderen Inhalt als Ihr Login-Schlüssel. Liegt Ihr Login-Schlüssel dagegen schon unter einem anderen Namen im Projekt, verwendet `start.sh` ihn einfach.  
**Lösung:** Löschen oder benennen Sie den alten Schlüssel in der Hetzner Console um, oder setzen Sie ein anderes `SERVER_PREFIX`.

### Meldung: "File not readable: … (in the container only ~/.ssh is visible)"

**Ursache:** Die Datei aus `GIT_DEPLOY_KEY_FILE` oder `IPAD_SSH_PUBLIC_KEY_FILE` fehlt oder ist nicht lesbar. Im Container ist außerhalb von `~/.ssh` keine Datei Ihres Home-Verzeichnisses sichtbar.  
**Lösung:** Prüfen Sie den Pfad. Beim Lauf im Container legen Sie die Datei unter `~/.ssh` ab und tragen diesen Pfad ein. Es wurde noch nichts angelegt.

### Meldung: "error validating token" beim Start

**Ursache:** `gh` hat das Token aus `VIPER_GH_TOKEN` abgelehnt: Es ist abgelaufen, widerrufen oder falsch kopiert. Ein klassisches Token (nicht fein granular) braucht außerdem mindestens die Scopes `repo` und `read:org`. Der Server ist schon angelegt.  
**Lösung:** Korrigieren Sie `VIPER_GH_TOKEN` und führen Sie `./start.sh <rechner>` erneut aus. Der vorhandene Server wird weiterverwendet.

### Meldung: "Git check on the server failed. NOT deleting server"

**Ursache:** `stop.sh` konnte die Git-Prüfung auf dem Server nicht ausführen, etwa wegen eines Verbindungsabbruchs.  
**Lösung:** Führen Sie `./stop.sh <rechner>` erneut aus. Ist der Server dauerhaft nicht erreichbar, nutzen Sie `./stop.sh <rechner> --force`.

### Meldung: "Existing DNS record is not managed by this setup; refusing overwrite"

**Ursache:** Unter diesem Namen existiert ein DNS-Eintrag, den die Skripte nicht angelegt haben.  
**Lösung:** Wählen Sie einen anderen Rechnernamen oder löschen Sie den fremden Eintrag in Cloudflare, falls er nicht mehr gebraucht wird.

### Meldung: "Multiple A records; refusing to modify"

**Ursache:** Es gibt mehrere A-Records mit diesem Namen.  
**Lösung:** Löschen Sie in Cloudflare alle überflüssigen Einträge.

### Meldung: "SSH unreachable. Use ./stop.sh <machine> --force …"

**Ursache:** `stop.sh` erreicht den Server nicht (Netz, Firewall oder der Server läuft nicht).  
**Lösung:** Prüfen Sie Netz und Firewall. Ist der Server nicht zu retten, nutzen Sie `./stop.sh <rechner> --force`.

### Meldung: "host key is not trusted" (bei `start.sh` oder `stop.sh`)

**Ursache:** Der Server antwortet, beweist aber nicht, dass er der erwartete Rechner ist. Mögliche Gründe:
* Der Rechner wurde von einem anderen Computer aus gelöscht und neu angelegt, und dieser Computer hat noch den Schlüssel des alten Servers hinterlegt. Das erkennt `start.sh` normalerweise an der geänderten Server-ID und verwirft den alten Schlüssel selbst.
* Auf diesem Computer ist kein Schlüssel hinterlegt, und das Zertifikat ist abgelaufen.
* Jemand sitzt zwischen Ihnen und dem Server.

**Lösung:** Nutzen Sie **nicht** einfach `--force`, denn dann prüft `stop.sh` keine ungesicherte Arbeit. Arbeiten Sie vom Computer aus, der den Rechner angelegt hat, oder prüfen Sie den Fingerabdruck in der Web-Konsole des Servers (`ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub`). Erst wenn Sie sicher sind, dass auf dem Server nichts Wichtiges liegt, löschen Sie ihn mit `./stop.sh <rechner> --force`.

### Meldung: "WARNING: DNS removal failed"

**Ursache:** Der DNS-Eintrag konnte nicht gelöscht werden, der Server ist aber gelöscht.  
**Lösung:** Entfernen Sie den A-Record in Cloudflare von Hand.

### `ping <rechner>.<domain>`: "Name oder Dienst nicht bekannt"

**Ursache:** Ihr DNS-Server merkt sich, dass es den Namen nicht gibt. Das passiert, wenn der Name abgefragt wurde, während der DNS-Eintrag nicht existierte, etwa zwischen `make down` und `make up`. Die Zone erlaubt das Zwischenspeichern dieser negativen Antwort für bis zu 30 Minuten (`make check` nennt den genauen Wert). Oft betrifft das den DNS-Server eines VPNs, während öffentliche Resolver den Namen schon auflösen.  
**Lösung:** Prüfen Sie mit `make check MACHINE=<rechner>`, ob Cloudflare 1.1.1.1 und Google 8.8.8.8 den Namen richtig auflösen. Dann liegt es nur an Ihrem DNS-Server: warten Sie bis zu 30 Minuten, oder nutzen Sie vorübergehend einen anderen DNS-Server (zum Beispiel ohne VPN). `resolvectl flush-caches` leert nur den Cache Ihres Computers, nicht den des DNS-Servers.

### `ping` bekommt keine Antwort

**Ursache:** Die Firewall des Rechners lässt kein ICMP durch. Ältere Firewalls hatten nur die SSH-Regel.  
**Lösung:** Führen Sie `make up MACHINE=<rechner>` aus. Das ergänzt die ICMP-Regel in der Firewall, der Server bleibt dabei bestehen. Ob der Rechner erreichbar ist, zeigt auch ohne Ping `make check MACHINE=<rechner>`.

### Meldung: "WARNING: some Homebrew taps, trust entries or formulae failed"

**Ursache:** Ein Tap, ein Trust-Eintrag, eine Formel oder die `Brewfile` ließ sich nicht einrichten, etwa wegen eines Tippfehlers im Namen, eines nicht erreichbaren Taps oder eines Build-Fehlers. Die Zeilen mit `FAILED` darüber nennen den Schritt.  
**Lösung:** Lesen Sie das Protokoll mit `make ssh MACHINE=<rechner> CMD="tail -n 50 ~/.config/viper/brew-packages.log"`, korrigieren Sie die Einstellung und führen Sie `make up MACHINE=<rechner>` erneut aus.

### Verbindung wird nach einigen Tagen abgelehnt

**Ursache:** Das Host-Zertifikat ist nach sieben Tagen abgelaufen. Eine automatische Erneuerung gibt es nicht. Computer mit hinterlegtem Host-Schlüssel verbinden sich weiter. Das ist der Computer, der den Rechner angelegt hat, und jeder andere, der innerhalb der sieben Tage einmal `start.sh` oder `stop.sh` ausgeführt hat (dabei wird der Schlüssel über die noch gültige CA-Prüfung nachgetragen). Betroffen sind Geräte, die nur der Host-CA vertrauen, etwa das iPad, und Computer, die den Rechner nie innerhalb der Gültigkeit erreicht haben.  
**Lösung:** Arbeiten Sie von einem Computer mit hinterlegtem Schlüssel aus, pushen Sie dort Ihre Arbeit, beenden Sie den Rechner mit `./stop.sh <rechner>` und starten Sie ihn neu.

## 8. FAQ

**Kann ich einen Rechner nur pausieren?**  
Nein. `stop.sh` löscht den Server samt Platte. `start.sh` richtet ihn neu ein und klont das Repository erneut.

**Was kostet es, wenn nichts läuft?**  
Nichts mehr. Ein gelöschter Server wird nicht berechnet. Prüfen Sie in der Hetzner Console, dass wirklich nichts übrig ist.

**Muss ich mich bei Claude nach jedem neuen Server neu anmelden?**  
Nur ohne `VIPER_CLAUDE_TOKEN`: Die Anmeldung mit `/login` liegt im Home-Verzeichnis des Servers und wird mit ihm gelöscht. Mit dem Token ist jeder neue Server sofort angemeldet (siehe „Claude Code ohne erneute Anmeldung nutzen“).

**Kann ich einen Rechner mit anderem Servertyp starten?**  
Ja. Setzen Sie `SERVER_TYPE` in `machines/<rechner>.env.local`. Bei einem bereits laufenden Server wirkt die Änderung erst nach Löschen und Neustart.

**Funktionieren bestehende Server und Volumes aus der Vorversion?**  
Nein. Die Namen haben sich geändert (seit Handbuch 1.8 auch DNS-Namen und lokale Dateinamen, jetzt mit Präfix), und es gibt keine separaten Volumes mehr. Löschen Sie alte Server mit der Version der Skripte, mit der Sie sie angelegt haben, und alte Volumes in der Hetzner Console, bevor Sie die neue Version verwenden.

**Darf ich die Tokens in die README oder in Git schreiben?**  
Nein. Verwenden Sie die Shell oder `.env.local`, die nicht eingecheckt wird.

## 9. Glossar

| Begriff | Bedeutung |
|---|---|
| Rechner | Ein Server mit Namen, in der Regel für ein Repository |
| Arbeitsverzeichnis | Verzeichnis auf dem Server, in dem die Repositories liegen (`WORKSPACE_DIR`) |
| Host-CA | Lokale Zertifizierungsstelle, mit der der Server-Schlüssel signiert wird |
| Host-Zertifikat | Beglaubigung des Server-Schlüssels, sieben Tage gültig |
| Hinterlegter Host-Schlüssel | Öffentlicher Schlüssel des Servers, den `start.sh` beim Anlegen speichert; Rückfall nach Ablauf des Zertifikats |
| PAT | Personal Access Token: Zugangsschlüssel für die GitHub-API anstelle eines Passworts |
| Tap | Zusätzliche Paketquelle für Homebrew (`owner/repo`) |
| Brewfile | Datei im Repository, in der Homebrew-Taps, Trust und Formeln stehen; `brew bundle` richtet sie ein |
| Deploy-Key | SSH-Schlüssel, der nur für ein Repository gilt |
| cloud-init | Automatische Ersteinrichtung des Servers beim ersten Start |
| tmux | Programm, das Terminal-Sitzungen dauerhaft hält |
| A-Record | DNS-Eintrag, der einen Namen auf eine IPv4-Adresse abbildet |
| Zone-ID | Kennung Ihrer Domain bei Cloudflare |

## 10. Support und Kontakt

Zu den Skripten gibt es keinen eigenen Support. Prüfen Sie bei Problemen zuerst Kapitel 7. Für Hetzner und Cloudflare gelten deren Dokumentation und Support. Beachten Sie: Echt gelaufen sind Einrichtung, Wiederverwenden, Neuanlegen, Check und Löschen auf Hetzner Cloud mit Cloudflare-DNS sowie Claude über das Abo-Token und Ollamas Cloud. Noch nicht echt gelaufen sind `VIPER_GH_TOKEN`/gh, Deploy-Keys, das iPad und ein vLLM-Server. Probieren Sie Neues in einem Testprojekt und mit `make preflight` aus.

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

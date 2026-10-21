# Benutzerhandbuch: Hetzner Viper

Version: 1.9  
Software-Version: v3 (mit Mehrrechner-Betrieb)  
Stand: 21.10.2026

## 1. Einleitung

### Zweck der Software

Die Skripte in diesem Repository richten mit einem Befehl einen entfernten Arbeitsrechner bei Hetzner Cloud ein. Auf diesem Rechner arbeitet der Code-Agent Claude Code als „virtuelle Person“ (VIPER: VIrtual PERson) in Ihrem Repository. Sie arbeiten darauf über VS Code (Remote-SSH) oder über ein SSH-Terminal, zum Beispiel vom iPad.

Pro Git-Repository richten Sie einen eigenen Rechner ein. Beim Einrichten wird das Repository automatisch geklont. Wenn Sie fertig sind, löschen Sie den Rechner wieder. Alle Dateien auf dem Rechner werden dabei gelöscht. Sichern Sie Ihre Arbeit deshalb vorher mit `git push`.

### Zielgruppe

Dieses Handbuch richtet sich an Entwicklerinnen und Entwickler, die auf einem Linux-Rechner arbeiten und die Kommandozeile bedienen können.

### Voraussetzungen

Auf Ihrem lokalen Rechner (nur Linux ist erprobt; unter macOS bitte den Container-Lauf nutzen):

* `bash`, `curl`, `jq`, `python3`, OpenSSH-Client
* die Hetzner-Kommandozeile `hcloud`
* VS Code mit der Erweiterung **Remote-SSH** (optional: der Befehl `code`)
* Alternativ zu den lokalen Werkzeugen: Docker und `make` (siehe Aufgabe „Rechner im Container einrichten und beenden“)

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
4. Führen Sie `./setup.sh` aus.
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
* **GitHub (wenn `VIPER_GH_TOKEN` gesetzt ist):** Das Token ist gültig, ein klassisches Token hat die Scopes `repo` und `read:org`, und es hat Zugriff auf ein GitHub-Repository aus `REPO_URL`.
* **Repository:** Ein HTTPS- oder lokales Repository ist erreichbar. SSH-Repositories prüft erst der Clone auf dem Server.

#### Ergebnis

`Preflight OK.` oder eine Liste der Probleme. Es wurde nichts angelegt oder geändert.

#### Hinweise

* Schreibrechte der Tokens (Hetzner: Server anlegen, Cloudflare: DNS bearbeiten) lassen sich nur lesend nicht prüfen. Fehlen sie, scheitert erst `start.sh`.
* Fehlen in `.env.local` die Werte `SERVER_TYPE`, `SERVER_LOCATION`, `SERVER_IMAGE` oder `SSH_KEY`, gelten die Standardwerte aus Kapitel 4.

### Erster Testlauf mit einem kleinen Server

Bevor Sie mit einem großen Server arbeiten, prüfen Sie die Einrichtung einmal mit dem kleinsten Typ. Ein Testlauf von etwa einer Stunde kostet nur Cent-Beträge, weil Hetzner stundenweise abrechnet.

#### Voraussetzung

Die erstmalige Einrichtung ist abgeschlossen. Am besten nutzen Sie ein eigenes Hetzner-Projekt nur für diese Rechner. Ein API-Token gilt immer für das ganze Projekt.

#### Vorgehen

1. Legen Sie für den Testrechner eine eigene Einstellungsdatei an:
   ```bash
   echo 'SERVER_TYPE=cpx02' > machines/test.env.local
   ```
   `cpx02` hat 1 vCPU, 1 GB RAM und 20 GB Platte.
2. Prüfen Sie vorab: `./preflight.sh test https://github.com/<konto>/<repo>.git`. Meldet die Prüfung, dass `cpx02` in `fsn1` derzeit nicht bestellbar ist, ergänzen Sie in derselben Datei `SERVER_LOCATION=nbg1` oder `hel1`, oder nehmen Sie `SERVER_TYPE=cpx12` (2 GB RAM).
3. Starten Sie den Rechner: `./start.sh test https://github.com/<konto>/<repo>.git`.
4. Prüfen Sie den Rechner:
   ```bash
   ssh hetzner-claude-test "bash -lc 'claude --version; gh --version | head -n1; docker info >/dev/null && echo docker ok; ls /Development'"
   ```
5. Löschen Sie den Rechner wieder: `./stop.sh test` und bestätigen Sie mit `DELETE`.

#### Ergebnis

Sie wissen, dass Tokens, DNS, SSH-Vertrauen, Einrichtung, Clone und Löschen in Ihrem Konto funktionieren. In der Hetzner Console und bei Cloudflare ist danach nichts mehr vom Testrechner übrig. SSH-Schlüssel und Firewall bleiben für die nächsten Rechner bestehen.

#### Hinweise

* 1 GB RAM reicht für den Funktionstest. Für echte Arbeit des Agenten mit Builds und Tests ist der Speicher zu knapp. Nutzen Sie dafür einen größeren Typ.
* Bricht `start.sh` ab, bleibt der Server bestehen. Führen Sie `./start.sh test` nach dem Beheben erneut aus, oder löschen Sie ihn mit `./stop.sh test`.

### Einen Rechner für ein Repository einrichten

#### Voraussetzung

Die erstmalige Einrichtung ist abgeschlossen und die drei Zugangsdaten sind gesetzt.

#### Vorgehen

1. Starten Sie den Rechner und geben Sie die Repository-URL mit an:
   ```bash
   ./start.sh web https://git.example.org/team/web.git
   ```
   Ohne URL wird `REPO_URL` aus `machines/web.env.local` verwendet (siehe Kapitel 4).
2. Warten Sie. Das Skript legt Server, Firewall und DNS-Eintrag an und wartet auf den Abschluss der Servereinrichtung. Das dauert meist einige Minuten, höchstens etwa 20.
3. VS Code öffnet sich automatisch mit dem geklonten Repository, sofern der Befehl `code` vorhanden ist. Sonst öffnen Sie die Verbindung `hetzner-claude-web` in VS Code Remote-SSH selbst.

#### Ergebnis

Die Meldung `Ready: ssh hetzner-claude-web (work dir: …)` erscheint. Das Repository liegt im Arbeitsverzeichnis unter dem Arbeitsverzeichnis (Standard `/Development`).

#### Hinweise

* Ein schon vorhandenes Repository wird beim erneuten Aufruf von `start.sh` für einen laufenden Server nicht erneut geklont.
* Das Host-Zertifikat des Servers ist sieben Tage gültig. Bei jedem neuen Server wird ein frisches Zertifikat erstellt. Eine manuelle Prüfung des Fingerabdrucks ist nicht nötig.
* Zusätzlich hinterlegt `start.sh` den Host-Schlüssel des Servers in `~/.ssh/known_hosts_hetzner_<SERVER_PREFIX>-<rechner>`. Die Skripte, `ssh` und VS Code vertrauen auf diesem Computer nur diesem Schlüssel. Ein Schlüssel eines früheren Servers wird abgelehnt, auch wenn sein Zertifikat noch gültig ist. Die Verbindung funktioniert auch nach Ablauf des Zertifikats. Fehlt der hinterlegte Schlüssel (Server auf einem anderen Computer angelegt), gilt bei der ersten Verbindung die Host-CA. Danach trägt das Skript den über diese Verbindung gelesenen Schlüssel als hinterlegten Schlüssel ein. Wird der Rechner neu angelegt (neue Server-ID), verwirft es einen alten hinterlegten Schlüssel.
* Schlägt die Installation von Claude Code bei der Einrichtung fehl, holt `start.sh` sie beim nächsten Aufruf nach.
* Die Skripte verbinden sich über die IP-Adresse des Servers. Den DNS-Eintrag brauchen nur Sie für andere Geräte (etwa das iPad). `start.sh` wartet deshalb nicht darauf, dass er weltweit sichtbar ist.

### Rechner im Container einrichten und beenden

Statt `hcloud`, `jq` und die übrigen Werkzeuge lokal zu installieren, können Sie die Skripte in einem Docker-Container ausführen. Sie brauchen dann nur Docker und `make`.

#### Voraussetzung

* `.env.local` ist angelegt (siehe erstmalige Einrichtung).
* Die drei Zugangsdaten sind in der Shell gesetzt (`HCLOUD_TOKEN`, `CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_ZONE_ID`).

#### Vorgehen

1. Einmalig: `make setup`
2. Rechner starten: `make up MACHINE=web REPO=https://git.example.org/team/web.git`
3. Rechner beenden: `make down MACHINE=web` (bei nicht erreichbarem Server `make down MACHINE=web FORCE=1`)

#### Ergebnis

Das Ergebnis ist dasselbe wie beim direkten Aufruf. Login-Schlüssel, Host-CA und SSH-Konfiguration liegen in Ihrem `~/.ssh`, weil der Container dieses Verzeichnis unter demselben Pfad einbindet. Eine Verbindung mit `ssh hetzner-claude-web` oder VS Code Remote-SSH funktioniert daher direkt vom Host aus.

#### Hinweise

* VS Code wird aus dem Container nicht gestartet. Öffnen Sie die Verbindung `hetzner-claude-web` selbst.
* `make up` baut das Image bei Bedarf und übernimmt Ihre Benutzerkennung und Ihr Home-Verzeichnis. Das Image gilt daher nur für Ihren Benutzer.
* Der Container sieht das Repository nur lesend.
* Von Ihrem Home-Verzeichnis sieht der Container nur `~/.ssh`. Legen Sie Dateien für `GIT_DEPLOY_KEY_FILE` und `IPAD_SSH_PUBLIC_KEY_FILE` deshalb dort ab. Sonst bricht `start.sh` mit „File not readable“ ab, bevor etwas angelegt wird.
* Die Skripte nutzen nur die SSH-Konfiguration des jeweiligen Rechners (`~/.ssh/config_hetzner_<SERVER_PREFIX>-<rechner>`). Einstellungen in Ihrer `~/.ssh/config` stören daher nicht.

### Mit Claude Code arbeiten

#### Vorgehen

1. Öffnen Sie in VS Code das integrierte Terminal.
2. Wechseln Sie in das Repository-Verzeichnis.
3. Starten Sie eine dauerhafte Sitzung und Claude Code:
   ```bash
   tmux new-session -A -s claude
   claude
   ```
4. Zum Verlassen der Sitzung drücken Sie **Strg+B**, dann **D**. Die Sitzung läuft weiter.
5. Verbinden Sie sich später von einem beliebigen SSH-Client erneut mit `tmux attach -t claude`.

#### Ergebnis

Claude Code läuft in einer Sitzung, die einen Verbindungsabbruch übersteht.

#### Hinweise

* Das Anmelden bei Claude müssen Sie nach jedem neuen Server wiederholen. Das Verzeichnis `/home/builder/.claude` wird mit dem Server gelöscht.

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
3. Starten Sie den Rechner mit `./start.sh <rechner>`.

#### Ergebnis

Das Repository wird geklont. Der Schlüssel liegt auf dem Server unter `~/.ssh/deploy_key` und ist für das Repository als `core.sshCommand` hinterlegt. So funktionieren später auch `git pull` und `git push` (sofern der Schlüssel Schreibrechte hat).

#### Hinweise

* Beim ersten Kontakt mit dem Git-Dienst wird dessen Host-Schlüssel ohne Rückfrage übernommen. Bei HTTPS-Adressen ist kein Schlüssel nötig.
* Der Schlüssel wird bei jedem Start erneut auf den Server kopiert.

### GitHub-CLI und Token auf dem Rechner nutzen

Auf jedem Rechner ist die GitHub-CLI `gh` installiert. Mit einem Personal Access Token (PAT) können Claude Code und Sie damit Pull Requests anlegen, Issues lesen und per HTTPS pushen.

#### Voraussetzung

Sie haben bei GitHub ein PAT erzeugt. Empfohlen ist ein **fein granulares** Token, das nur für das Repository des Rechners gilt, mit kurzer Laufzeit und nur den nötigen Rechten (zum Beispiel **Contents** und **Pull requests**: Lesen und Schreiben).

#### Vorgehen

1. Tragen Sie das Token als `VIPER_GH_TOKEN` ein:
   * für einen einzelnen Rechner in `machines/<rechner>.env.local` (empfohlen), oder
   * für alle Rechner in `.env.local` oder als `export VIPER_GH_TOKEN=...` in der Shell.
2. Für GitHub Enterprise setzen Sie zusätzlich `VIPER_GH_HOST` auf den Hostnamen.

Die Skripte lesen bewusst nicht `GH_TOKEN`. Ein Token, das Sie für Ihr lokales `gh` exportiert haben, landet so nie ungewollt auf einem Server.
3. Starten Sie den Rechner mit `./start.sh <rechner>` bzw. `make up MACHINE=<rechner>`.

#### Ergebnis

Die Meldung `gh: logged in to github.com` erscheint. Auf dem Server ist `gh` angemeldet, und `git` nutzt das Token für HTTPS-Adressen. Private Repositories können Sie daher auch per HTTPS-URL klonen lassen.

#### Hinweise

* Das Token wird per SSH über die Standardeingabe übertragen. Es steht weder in den Hetzner-User-Data noch in einer Befehlszeile.
* Auf dem Server liegt das Token unverschlüsselt in `~/.config/gh/hosts.yml`. Claude Code und jeder Prozess des Benutzers `builder` können es lesen. Vergeben Sie deshalb nur die Rechte, die der Agent braucht.
* Mit dem Löschen des Servers ist das Token dort entfernt. Bei GitHub bleibt es gültig, bis es abläuft oder Sie es widerrufen.

### Vom iPad aus verbinden

#### Vorgehen

1. Erzeugen Sie auf dem iPad ein SSH-Schlüsselpaar und speichern Sie den öffentlichen Schlüssel als Datei auf Ihrem lokalen Rechner.
2. Tragen Sie den Pfad bei `IPAD_SSH_PUBLIC_KEY_FILE` in `.env.local` ein.
3. Starten Sie einen neuen Rechner. Der Schlüssel wird für den Benutzer `builder` hinterlegt.
4. Verbinden Sie sich mit Benutzer `builder` und dem DNS-Namen des Rechners.

#### Hinweise

* Bei einem bereits laufenden Server tragen Sie den Schlüssel selbst in `/home/builder/.ssh/authorized_keys` ein.
* Die Prüfung des Server-Zertifikats funktioniert nur, wenn die iPad-App Host-Zertifikate unterstützt. Die öffentliche Host-CA gibt `setup.sh` am Ende aus.
* Erlaubt die Firewall Ihr Netz nicht, kommen Sie nicht durch (siehe Kapitel 4, Firewall).

### Einen Rechner beenden

#### Voraussetzung

Schließen Sie alle VS-Code-Fenster und Terminals, die auf dem Rechner geöffnet sind, und beenden Sie die tmux-Sitzungen.

#### Vorgehen

1. Führen Sie `./stop.sh <rechner>` aus.
2. Lesen Sie die Ausgabe: Das Skript zeigt je Repository und je Worktree den Git-Stand, Commits, die auf keinem Remote liegen (auf allen lokalen Branches), und die tmux-Sitzungen. Es sucht Repositories im Arbeitsverzeichnis, mit dem der Rechner gestartet wurde, im Home-Verzeichnis von `builder` und in `/tmp` und `/var/tmp`. Dateien und Ordner direkt im Arbeitsverzeichnis, die zu keinem Repository gehören, listet es unter **Not in any git repository (lost)** auf. Dateien außerhalb von Repositories im Home-Verzeichnis oder in `/tmp` prüft es nicht. Gibt es ungesicherte Arbeit (uncommittete Änderungen, Commits auf keinem Remote, mit `git stash` beiseitegelegte Änderungen, Dateien außerhalb eines Repositorys, ein fehlendes Arbeitsverzeichnis oder ein nicht lesbares Repository), steht `UNSAVED WORK` in der Ausgabe, und Sie müssen zusätzlich `DISCARD` eingeben, um fortzufahren. Von Git ignorierte Dateien (zum Beispiel `.env` oder Build-Verzeichnisse) werden unter **Ignored by git (also lost)** aufgelistet, blockieren aber nicht.
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
| `SERVER_TYPE` | Hetzner-Servertyp | `cx53` |
| `SERVER_LOCATION` | Standort | `fsn1` |
| `SERVER_IMAGE` | Betriebssystem-Image | `ubuntu-26.04` |
| `DNS_DOMAIN` | Ihre Domain, Pflichtangabe | leer |
| `SSH_KEY` | Pfad Ihres Login-Schlüssels | `~/.ssh/hetzner-build` |
| `SSH_ALLOWED_CIDRS` | Netze, die Port 22 erreichen dürfen | `0.0.0.0/0,::/0` |
| `IPAD_SSH_PUBLIC_KEY_FILE` | Öffentlicher Schlüssel des iPads | leer |
| `WORKSPACE_DIR` | Verzeichnis auf dem Server, in das die Repositories geklont werden | `/Development` |
| `REPO_URL` | Repository, das beim Start geklont wird | leer |
| `REPO_BRANCH` | Zweig, der geklont wird | Standardzweig |
| `GIT_DEPLOY_KEY_FILE` | Deploy-Key für private SSH-Repositories | leer |
| `VIPER_GH_TOKEN` | GitHub-PAT für `gh` und `git` über HTTPS auf dem Server | leer |
| `VIPER_GH_HOST` | GitHub-Host (für GitHub Enterprise) | `github.com` |
| `HCLOUD_TOKEN`, `CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_ZONE_ID` | Zugangsdaten (Shell oder `.env.local`), Pflichtangaben | leer |

### Firewall und Anmeldung

* Die Firewall erhält ihre Regel aus `SSH_ALLOWED_CIDRS` nur, solange sie noch keine Regeln hat (beim ersten Anlegen oder wenn das Anlegen der Regel zuvor gescheitert ist). Bei einer Firewall mit Regeln ändern Sie die Regel für Port 22 in der Hetzner Console.
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
**Lösung:** Tragen Sie die Werte in `.env.local` oder `machines/<rechner>.env.local` ein oder exportieren Sie sie in der Shell. Beachten Sie: Eine leere Zeile `HCLOUD_TOKEN=` in `.env.local` überschreibt den Wert aus der Shell.

### Meldung: "DNS_DOMAIN must be a domain like example.com"

**Ursache:** `DNS_DOMAIN` enthält Großbuchstaben, ein Schema wie `https://`, einen Pfad oder einen Punkt am Ende.  
**Lösung:** Tragen Sie nur den Domainnamen ein, zum Beispiel `example.com`.

### Meldung: "Preflight: … problem(s); nothing was created."

**Ursache:** Die Vorabprüfung hat Probleme gefunden. Die Zeilen mit `FAIL` darüber nennen sie. Häufige Fälle:
* `… cannot currently be ordered in …`: Der Servertyp ist am Standort ausverkauft oder dort nicht erhältlich. Setzen Sie `SERVER_LOCATION` (etwa `nbg1` oder `hel1`) oder einen anderen `SERVER_TYPE`, am besten in `machines/<rechner>.env.local`.
* `server type … does not exist`, `location … does not exist`, `image … does not exist`: Tippfehler oder ein Wert, den Hetzner nicht anbietet. Die gültigen Werte zeigen `hcloud server-type list`, `hcloud location list` und `hcloud image list --type system`.
* `DNS_DOMAIN … is not in Cloudflare zone …`: `CLOUDFLARE_ZONE_ID` gehört zu einer anderen Domain.
* `repository … not reachable without credentials`: Das Repository ist privat. Setzen Sie `VIPER_GH_TOKEN` (GitHub) oder nutzen Sie eine SSH-URL mit `GIT_DEPLOY_KEY_FILE`.

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

**Ursache:** Der Server ist nach etwa 20 Minuten nicht per SSH erreichbar. Mögliche Gründe: Die Firewall blockiert Ihr Netz, der Server startet nicht, oder sein Host-Schlüssel passt nicht zum hinterlegten.  
**Lösung:**
1. Prüfen Sie die Firewall-Regel für Port 22 in der Hetzner Console.
2. Öffnen Sie die Web-Konsole des Servers und lesen Sie `/var/log/cloud-init-output.log`.
3. Starten Sie `./start.sh <rechner>` erneut, wenn die Ursache behoben ist.
4. Hilft das nicht, löschen Sie den Rechner mit `./stop.sh <rechner> --force` und legen Sie ihn neu an.

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

### Verbindung wird nach einigen Tagen abgelehnt

**Ursache:** Das Host-Zertifikat ist nach sieben Tagen abgelaufen. Eine automatische Erneuerung gibt es nicht. Computer mit hinterlegtem Host-Schlüssel verbinden sich weiter. Das ist der Computer, der den Rechner angelegt hat, und jeder andere, der innerhalb der sieben Tage einmal `start.sh` oder `stop.sh` ausgeführt hat (dabei wird der Schlüssel über die noch gültige CA-Prüfung nachgetragen). Betroffen sind Geräte, die nur der Host-CA vertrauen, etwa das iPad, und Computer, die den Rechner nie innerhalb der Gültigkeit erreicht haben.  
**Lösung:** Arbeiten Sie von einem Computer mit hinterlegtem Schlüssel aus, pushen Sie dort Ihre Arbeit, beenden Sie den Rechner mit `./stop.sh <rechner>` und starten Sie ihn neu.

## 8. FAQ

**Kann ich einen Rechner nur pausieren?**  
Nein. `stop.sh` löscht den Server samt Platte. `start.sh` richtet ihn neu ein und klont das Repository erneut.

**Was kostet es, wenn nichts läuft?**  
Nichts mehr. Ein gelöschter Server wird nicht berechnet. Prüfen Sie in der Hetzner Console, dass wirklich nichts übrig ist.

**Warum muss ich mich bei Claude jedes Mal neu anmelden?**  
Die Anmeldedaten liegen im Home-Verzeichnis des Servers und werden mit dem Server gelöscht.

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
| Deploy-Key | SSH-Schlüssel, der nur für ein Repository gilt |
| cloud-init | Automatische Ersteinrichtung des Servers beim ersten Start |
| tmux | Programm, das Terminal-Sitzungen dauerhaft hält |
| A-Record | DNS-Eintrag, der einen Namen auf eine IPv4-Adresse abbildet |
| Zone-ID | Kennung Ihrer Domain bei Cloudflare |

## 10. Support und Kontakt

Zu den Skripten gibt es keinen eigenen Support. Prüfen Sie bei Problemen zuerst Kapitel 7. Für Hetzner und Cloudflare gelten deren Dokumentation und Support. Beachten Sie: Die Skripte wurden nicht gegen ein echtes Hetzner- und Cloudflare-Konto getestet. Probieren Sie sie in einem Testprojekt aus und prüfen Sie `hcloud --help`, falls Ihre Version abweicht.

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
| 1.9 | v3 mit Mehrrechner-Betrieb | Das GitHub-Token heißt jetzt `VIPER_GH_TOKEN` (statt `GH_TOKEN`), ein lokal exportiertes `GH_TOKEN` wird nicht mehr übernommen. Ein abgelehnter Host-Schlüssel wird als solcher gemeldet statt als „nicht erreichbar“, und `stop.sh --force` prüft einen erreichbaren Server trotzdem. Hinterlegte Schlüssel werden bei neuer Server-ID verworfen und nach einer CA-geprüften Verbindung nachgetragen. Eine als Symlink verwaltete `~/.ssh/config` bleibt erhalten. Neu: Abschnitt „Erster Testlauf mit einem kleinen Server“. `preflight.sh` prüft vorab nur lesend, und `start.sh` führt es zuerst aus. Fehlende Pflichtwerte werden gemeinsam gemeldet, bevor etwas angelegt oder gelöscht wird. |

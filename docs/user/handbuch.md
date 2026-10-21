# Benutzerhandbuch: Hetzner Viper

Version: 1.4  
Software-Version: v3 (mit Mehrrechner-Betrieb)  
Stand: 21.10.2026

## 1. Einleitung

### Zweck der Software

Die Skripte in diesem Repository richten mit einem Befehl einen entfernten Arbeitsrechner bei Hetzner Cloud ein. Auf diesem Rechner arbeitet der Code-Agent Claude Code als „virtuelle Person“ (VIPER: VIrtual PERson) in Ihrem Repository. Sie arbeiten darauf über VS Code (Remote-SSH) oder über ein SSH-Terminal, zum Beispiel vom iPad.

Pro Git-Repository richten Sie einen eigenen Rechner ein. Beim Einrichten wird das Repository automatisch geklont. Wenn Sie fertig sind, löschen Sie den Rechner wieder. Alle Dateien auf dem Rechner werden dabei gelöscht. Sichern Sie Ihre Arbeit deshalb vorher mit `git push`.

### Zielgruppe

Dieses Handbuch richtet sich an Entwicklerinnen und Entwickler, die auf einem Linux-Rechner arbeiten und die Kommandozeile bedienen können.

### Voraussetzungen

Auf Ihrem lokalen Linux-Rechner:

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
| `start.sh <rechner>` | Richtet einen Rechner ein, setzt den DNS-Eintrag, legt das Arbeitsverzeichnis an und klont das Repository. |
| `stop.sh <rechner>` | Prüft den Git-Stand, warnt vor ungesicherter Arbeit und löscht Server und DNS-Eintrag. |
| `dns.sh` | Verwaltet nur den eigenen DNS-Eintrag. Sie rufen es normalerweise nicht selbst auf. |

### Wie ein Rechner benannt wird

Jeder Rechner hat einen Namen, den Sie beim Start angeben (`<rechner>`). Erlaubt sind Kleinbuchstaben, Ziffern und Bindestriche. Der Name darf nicht mit einem Bindestrich beginnen oder enden. Aus dem Namen entstehen:

| Ressource | Name |
|---|---|
| Server | `<SERVER_PREFIX>-<rechner>` |
| DNS-Name | `<rechner>.<DNS_DOMAIN>` |
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

### Einen Rechner für ein Repository einrichten

#### Voraussetzung

Die erstmalige Einrichtung ist abgeschlossen und die drei Zugangsdaten sind gesetzt.

#### Vorgehen

1. Starten Sie den Rechner und geben Sie die Repository-URL mit an:
   ```bash
   ./start.sh web https://git.example.org/team/web.git
   ```
   Ohne URL wird `REPO_URL` aus `machines/web.env.local` verwendet (siehe Kapitel 4).
2. Warten Sie. Das Skript legt Server, Firewall und DNS-Eintrag an und wartet auf den Abschluss der Servereinrichtung. Das dauert einige Minuten.
3. VS Code öffnet sich automatisch mit dem geklonten Repository, sofern der Befehl `code` vorhanden ist. Sonst öffnen Sie die Verbindung `hetzner-claude-web` in VS Code Remote-SSH selbst.

#### Ergebnis

Die Meldung `Ready: ssh hetzner-claude-web (work dir: …)` erscheint. Das Repository liegt im Arbeitsverzeichnis unter dem Arbeitsverzeichnis (Standard `/Development`).

#### Hinweise

* Ein schon vorhandenes Repository wird beim erneuten Aufruf von `start.sh` für einen laufenden Server nicht erneut geklont.
* Das Host-Zertifikat des Servers ist sieben Tage gültig. Bei jedem neuen Server wird ein frisches Zertifikat erstellt. Eine manuelle Prüfung des Fingerabdrucks ist nicht nötig.

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
* Die Skripte nutzen nur die SSH-Konfiguration des jeweiligen Rechners (`~/.ssh/config_hetzner_claude_<rechner>`). Einstellungen in Ihrer `~/.ssh/config` stören daher nicht.

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
2. Lesen Sie die Ausgabe: Das Skript zeigt je Repository den Git-Stand, nicht gepushte Commits, tmux-Sitzungen. Gibt es ungesicherte Arbeit (uncommittete, nicht gepushte oder mit `git stash` beiseitegelegte Änderungen, Repository ohne Upstream oder ein nicht lesbares Repository), steht `UNSAVED WORK` in der Ausgabe, und Sie müssen zusätzlich `DISCARD` eingeben, um fortzufahren. Von Git ignorierte Dateien (zum Beispiel `.env` oder Build-Verzeichnisse) werden unter **Ignored by git (also lost)** aufgelistet, blockieren aber nicht.
3. Sichern Sie offene Änderungen (committen und pushen), falls nötig, und starten Sie `stop.sh` erneut.
4. Geben Sie zur Bestätigung `DELETE` ein.

#### Ergebnis

Server, DNS-Eintrag und die lokale SSH-Konfiguration des Rechners sind gelöscht. Alle Dateien auf dem Server sind weg. Beim nächsten `./start.sh <rechner>` wird das Repository neu geklont.

#### Hinweise

* Auch Ihre Claude-Anmeldung und alle nicht gepushten Änderungen gehen verloren.
* Das Skript bricht ab, wenn Sie `DELETE` nicht eingeben.
* Haben Sie den Server schon in der Hetzner Console gelöscht, räumt `./stop.sh <rechner>` nur noch DNS-Eintrag und SSH-Konfiguration auf.

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
| `SERVER_IMAGE` | Betriebssystem-Image | `ubuntu-24.04` |
| `DNS_DOMAIN` | Ihre Domain, Pflichtangabe | leer |
| `SSH_KEY` | Pfad Ihres Login-Schlüssels | `~/.ssh/hetzner-build` |
| `SSH_ALLOWED_CIDRS` | Netze, die Port 22 erreichen dürfen | `0.0.0.0/0,::/0` |
| `IPAD_SSH_PUBLIC_KEY_FILE` | Öffentlicher Schlüssel des iPads | leer |
| `WORKSPACE_DIR` | Verzeichnis auf dem Server, in das die Repositories geklont werden | `/Development` |
| `REPO_URL` | Repository, das beim Start geklont wird | leer |
| `REPO_BRANCH` | Zweig, der geklont wird | Standardzweig |
| `GIT_DEPLOY_KEY_FILE` | Deploy-Key für private SSH-Repositories | leer |
| `HCLOUD_TOKEN`, `CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_ZONE_ID` | Zugangsdaten (Shell oder `.env.local`) | leer |

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

### Meldung: "Set DNS_DOMAIN in .env.local", "Set HCLOUD_TOKEN" oder ähnlich

**Ursache:** Eine Pflichtangabe oder ein Zugangswert fehlt.  
**Lösung:** Tragen Sie den Wert in `.env.local` ein oder exportieren Sie ihn in der Shell. Beachten Sie: Eine leere Zeile `HCLOUD_TOKEN=` in `.env.local` überschreibt den Wert aus der Shell.

### Meldung: "Machine name required" oder "Machine name: lowercase letters, digits, hyphens"

**Ursache:** Der Rechnername fehlt oder enthält ungültige Zeichen.  
**Lösung:** Geben Sie einen Namen aus Kleinbuchstaben, Ziffern und Bindestrichen an, zum Beispiel `./start.sh web`.

### Meldung: "Run ./setup.sh first"

**Ursache:** Login-Schlüssel oder Host-CA fehlen.  
**Lösung:** Führen Sie `./setup.sh` aus.

### Meldung: "DNS not yet propagated; server retained."

**Ursache:** Der DNS-Name zeigt noch nicht auf die neue IP-Adresse (nach 5 Minuten Wartezeit).  
**Lösung:**
1. Prüfen Sie in Cloudflare, ob der A-Record angelegt wurde.
2. Warten Sie kurz und führen Sie `./start.sh <rechner>` erneut aus. Der vorhandene Server wird weiterverwendet.

### Meldung: "SSH/cloud-init not ready; server retained."

**Ursache:** Der Server ist nach 10 Minuten nicht per SSH erreichbar oder die Einrichtung ist nicht abgeschlossen. Mögliche Gründe: Die Firewall blockiert Ihr Netz, die Servereinrichtung ist mit Fehlern abgebrochen, die Installation von Claude Code ist fehlgeschlagen oder das Zertifikat wird nicht akzeptiert. Warnungen von cloud-init allein führen nicht zu dieser Meldung.  
**Lösung:**
1. Prüfen Sie die Firewall-Regel für Port 22 in der Hetzner Console.
2. Öffnen Sie die Web-Konsole des Servers und lesen Sie `/var/log/cloud-init-output.log`.
3. Starten Sie `./start.sh <rechner>` erneut, wenn die Ursache behoben ist.
4. Hilft das nicht, löschen Sie den Rechner mit `./stop.sh <rechner> --force` und legen Sie ihn neu an.

### Meldung: "File not readable: … (in the container only ~/.ssh is visible)"

**Ursache:** Die Datei aus `GIT_DEPLOY_KEY_FILE` oder `IPAD_SSH_PUBLIC_KEY_FILE` fehlt oder ist nicht lesbar. Im Container ist außerhalb von `~/.ssh` keine Datei Ihres Home-Verzeichnisses sichtbar.  
**Lösung:** Prüfen Sie den Pfad. Beim Lauf im Container legen Sie die Datei unter `~/.ssh` ab und tragen diesen Pfad ein. Es wurde noch nichts angelegt.

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

**Ursache:** `stop.sh` erreicht den Server nicht.  
**Lösung:** Prüfen Sie Netz und Firewall. Ist der Server nicht zu retten, nutzen Sie `./stop.sh <rechner> --force`.

### Meldung: "WARNING: DNS removal failed"

**Ursache:** Der DNS-Eintrag konnte nicht gelöscht werden, der Server ist aber gelöscht.  
**Lösung:** Entfernen Sie den A-Record in Cloudflare von Hand.

### Verbindung wird nach einigen Tagen abgelehnt

**Ursache:** Das Host-Zertifikat ist nach sieben Tagen abgelaufen. Eine automatische Erneuerung gibt es nicht.  
**Lösung:** Pushen Sie Ihre Arbeit, beenden Sie den Rechner mit `./stop.sh <rechner>` und starten Sie ihn neu.

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
Nein. Die Namen haben sich geändert, und es gibt keine separaten Volumes mehr. Löschen Sie alte Server mit der alten Version der Skripte und alte Volumes in der Hetzner Console, bevor Sie die neue Version verwenden.

**Darf ich die Tokens in die README oder in Git schreiben?**  
Nein. Verwenden Sie die Shell oder `.env.local`, die nicht eingecheckt wird.

## 9. Glossar

| Begriff | Bedeutung |
|---|---|
| Rechner | Ein Server mit Namen, in der Regel für ein Repository |
| Arbeitsverzeichnis | Verzeichnis auf dem Server, in dem die Repositories liegen (`WORKSPACE_DIR`) |
| Host-CA | Lokale Zertifizierungsstelle, mit der der Server-Schlüssel signiert wird |
| Host-Zertifikat | Beglaubigung des Server-Schlüssels, sieben Tage gültig |
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

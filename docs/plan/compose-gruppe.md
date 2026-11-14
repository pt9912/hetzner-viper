# Plan: Eine Compose-Datei je Gruppe (Docker Compose)

**Ziel:** Die Rechner einer Gruppe (`groups/<gruppe>.env`, `PROVIDER=compose`) liegen in **einem** Compose-Projekt mit **einer** `compose.yaml`, nicht je Rechner in einem eigenen Projekt. Starten, Stoppen und Löschen einzelner Rechner bleiben möglich: `docker compose up -d <dienst>`, `stop <dienst>` und `rm -sf <dienst>` wirken auf einen Dienst und lassen die anderen unberührt.

**Stand:** gebaut (14.11.2026, Meilensteine 1 bis 5 in einem Zug, Ergebnis §9), einmal gegen den Code und Docker Compose v5.7 geprüft (§8). Ausgangspunkt ist [`compose.md`](compose.md) (§3: „eine Maschine = ein Projekt“, D2). Dieser Plan ersetzt D2 für Rechner in einer Gruppe.

## 1 Warum

* Ein Blick in `~/.local/state/viper/compose/` zeigt heute ein Verzeichnis je Rechner mit je einer Datei. Eine Gruppe von drei Rechnern ist drei Projekte.
* Eine Datei je Gruppe lässt sich von Hand mit `docker compose -p viper-g-<gruppe> ps` und `logs` ansehen.
* Das Netz der NATS-Gruppe, die Rechner und der Docker-Zwilling (`<rechner>-docker`) stehen an einer Stelle.

## 2 Was sich nicht ändert

* Der Container und das Volume eines Rechners heißen weiter `<servername>`, `<servername>-home`, `<servername>-brew`, `<servername>-docker`. Docker-Befehle von `viper` (`exec`, `inspect`, `volume rm`) und der `docker-proxy` bleiben gleich.
* Rechner **ohne Gruppe** behalten ihr eigenes Projekt `viper-<servername>` und ihre eigene Datei.
* Der Dienstname in der Datei der Gruppe ist der Servername (`<präfix>-<rechner>`), nicht mehr `machine`.
* Die Schlüsselableitung (Host-Schlüssel, erlaubte Schlüssel) bleibt: Dateien, die per Compose-`secrets` in den Container kommen. Sie liegen nun je Rechner unter `compose/<projekt>/secret/<servername>/`.

## 3 Abbildung

**Schlüssel des Projekts:** Der Projektschlüssel einer Gruppe ist `g_<gruppe>`, der eines Rechners ohne Gruppe sein Servername. Ein Unterstrich kommt in Servernamen und Gruppennamen nicht vor (`checkName`, Gruppenname wie bei `CheckNATSName`), so fallen die beiden nie zusammen (ein Präfix wie `g-` täte es nicht: ein Rechner `g-web` hätte dasselbe Projekt wie die Gruppe `web`). Der Gruppenname geht in Pfade und wird vorher geprüft.

| Heute | Neu (Rechner in einer Gruppe) |
|---|---|
| Projekt `viper-<servername>` | Projekt `viper_g_<gruppe>` (`-p`; kein `name:` mehr in der Datei) |
| Verzeichnis `compose/<servername>/` | `compose/g_<gruppe>/` mit `compose.yaml` |
| Schlüssel `compose/<servername>/secret/` | `compose/g_<gruppe>/secret/<servername>/` (in der Datei `./secret/<servername>/<datei>`) |
| Sperre `compose/<servername>.lock` | `compose/g_<gruppe>.lock` |
| Marke `deleting` im Verzeichnis | `compose/g_<gruppe>/deleting-<servername>` |
| Dienst `machine`, `network_mode: service:machine`, `depends_on: machine` | Dienst `<servername>`, `network_mode: service:<servername>`, `depends_on: <servername>` |
| Schlüssel in der Datei: Volumes `home` und `brew`, Secrets `authorized_keys` usw., Netz `group` | je Rechner eigene Schlüssel: `<servername>-home`, `<servername>-brew`, Secrets `<servername>-authorized_keys` usw.; das Gruppennetz `group` ist für alle gleich |
| Dienst `<servername>-docker` | unverändert, Container `<servername>-docker` |

Container, Volumes und Sidecar tragen zusätzlich das Label `viper-project=<schlüssel>`. Ein Container **ohne** dieses Label ist ein Rechner im alten Layout (§5).

## 4 Ablauf

* **Dateiänderung unter Sperre, `up` außerhalb.** Die Sperre der Gruppe deckt nur das Lesen, Zusammenführen und **atomare Schreiben** der Datei (Temp-Datei und `rename`) und die Entscheidung „war das der letzte Dienst“. Das `up --wait` (bis zu zehn Minuten) läuft **ohne** die Sperre, sonst würde `--parallel` für eine Gruppe seriell. Das ist sicher: `docker compose up` liest die Datei einmal beim Start, und zwei `up` verschiedener Dienste desselben Projekts liefen im Versuch gleichzeitig (4 von 4) fehlerfrei.
* **`CreateServer`:** unter der Sperre die Datei einlesen (`yaml.v3` in `composeFile`, verlustfrei, weil die Typen ausgeschrieben sind; von Hand eingefügte Felder gehen verloren, das steht im Handbuch), den Dienst samt Sidecar, Volumes und Secrets des Rechners **ersetzen oder hinzufügen**, die Schlüssel nach `secret/<servername>/` schreiben, die Datei schreiben, die Sperre lösen. Dann `docker compose -p viper_g_<gruppe> up -d --no-recreate --wait <servername> [<servername>-docker]`. Die anderen Dienste bleiben unberührt (verifiziert: Container-ID und Startzeit unverändert, auch wenn sich ihre Einstellungen in der Datei geändert haben; `network_mode: service:` und `depends_on … restart: true` lösen nichts aus).
* **`EnsureRunning`:** prüft, dass der **Dienst** in der Datei steht (nicht nur die Datei), sonst `up -d --no-recreate --wait <dienst>`. `repairDocker` bleibt nötig: ein einzeln gestoppter Rechner startet den Sidecar nicht mit neu (verifiziert).
* **`DeleteServer`:** Marke `deleting-<servername>`; ist der Dienst in der Datei: `docker compose -p … rm -sfv <servername> <servername>-docker` (beide nennen, sonst bleibt der Sidecar; `-v` nimmt auch das anonyme Volume `/var/lib/docker` des Sidecars mit, verifiziert); steht er nicht darin (Absturz zwischen `rm` und Dateiänderung, Datei fehlt): `docker rm -f -v` auf beide Container, dann `daemonVolume`/`removeDaemonVolume` als Rückfall. Danach die Volumes des Rechners löschen. **Zuletzt** unter der Sperre den Dienst aus der Datei nehmen und `secret/<servername>/` und die Marke löschen; sind danach keine Dienste und keine Schlüsselverzeichnisse mehr da, geht das Verzeichnis der Gruppe weg. Reihenfolge: erst Container, dann Datei (ein `rm` auf einen Dienst, der nicht in der Datei steht, scheitert mit „no such service“).
* **Nie `os.RemoveAll` auf das Verzeichnis der Gruppe** ohne die Entscheidung unter der Sperre: Heute rufen `dropStale`, `DeleteServer` und `DeleteLeftovers` es auf; bei einer Gruppe würden sie die gemeinsame Datei und die Schlüssel der anderen Rechner löschen (ein `--parallel`-Lauf ließe so den `up` eines anderen Rechners scheitern). Aus jedem Aufruf wird „`secret/<servername>/` und den Dienstteintrag entfernen“.
* **`Leftovers`/`DeleteLeftovers`:** Ein Rest ist ein Dienst oder Schlüsselverzeichnis `secret/<servername>/` **ohne** Container, dazu Volumes und Sidecar. „Files <Verzeichnis>“ gilt nur für Rechner ohne Gruppe; bei einer Gruppe zählt das Verzeichnis der Gruppe nicht als Rest, solange ein anderer Rechner darin lebt.
* **Wie `viper` das Projekt eines Namens findet** (`EnsureRunning(name)`, `Leftovers(name)`, `DeleteLeftovers(name)` bekommen weder Gruppe noch ID): aus dem Label `viper-project` des Containers, sonst der Volumes; gibt es weder Container noch Volumes, sucht es `compose/*/secret/<servername>` und `compose/<servername>`. Die Schnittstelle bleibt gleich.
* **Gruppenname:** `ServerSpec.Group` (neu, aus `Machine.Group`) trägt ihn zum Anbieter; leer heißt Rechner ohne Gruppe und eigenes Projekt.

## 5 Vorhandene Rechner und Wechsel der Gruppe

* **Zwei Wege im Code bleiben**, bis es kein altes Layout mehr gibt: der alte (`compose/<servername>/`, Sperre `<servername>.lock`, `down`, Dienst `machine`) und der neue. Welcher gilt, sagt das Label `viper-project` des Containers (fehlt es: alt).
* **Vorhandene Rechner** laufen mit dem alten Weg weiter, es gibt **keinen** automatischen Umzug (er würde den Container neu anlegen). Ein `viper up` für einen Rechner einer Gruppe, der im alten Layout läuft, nimmt den alten Weg (`EnsureRunning`); die Datei der Gruppe erhält ihn nie, und `Leftovers` und die Entscheidung „letzter Dienst“ der Gruppe übergehen ihn.
* **Gruppenwechsel:** Läuft der Rechner (Marke `group-<servername>`, [`gruppen.md`](gruppen.md)), lehnt `viper` ab. Im Anbieter zeigt das Label `viper-project` die Gruppe (`g_<gruppe>`); Rechner ohne Gruppe tragen es mit ihrem Servernamen.

## 6 Meilensteine

1. **Gerüst:** `ServerSpec.Group`, das Label `viper-project`, `projectKey(name, group)` und die Pfadfunktionen dahinter (`projectDir`, `composeFile`, `lock`, `markerDeleting`, `compose()` mit `-p`). Für Rechner ohne Gruppe bleiben Dateien und Befehle gleich; die Tests des Fake-Docker, die Pfade und `down` fest eintragen (`compose_test.go`), werden auf die neuen Funktionen umgestellt.
2. **Test-Fake:** Der Fake-Docker versteht `-p`, `rm -sfv` und pro Dienst ein Modell (Container je Dienst, nicht je Projekt). Vorher gebaut, damit die folgenden Schritte testbar sind.
3. **Gemeinsame Datei:** `composeFile` einlesen und zusammenführen, je Rechner eigene Schlüssel in `fileFor` (§3), atomares Schreiben, Sperre nur für die Dateiänderung, `CreateServer` und `EnsureRunning` für Gruppen.
4. **Löschen und Reste:** `DeleteServer` mit `rm -sfv` und Rückfall, Dienst und Schlüssel des Rechners entfernen, das Verzeichnis erst mit dem letzten, `Leftovers`/`DeleteLeftovers`, `dropStale`; kein `RemoveAll` auf das Verzeichnis der Gruppe außerhalb dieser Entscheidung.
5. **NATS-Gruppe und Parallelität:** Server und Clients einer NATS-Gruppe in einer Datei, mehrere `viper up` einer Gruppe gleichzeitig (Test mit gleichzeitigen Läufen und einem `down` dazwischen).
6. **Handbuch und Probelauf:** Handbuch (Docker Compose, „was `viper` anlegt“: eine Datei je Gruppe, Hinweis auf verlorene Handänderungen), `developer.md`, Lauf mit drei Rechnern gegen das lokale Docker: `viper up --group`, einzelnes `down`, `down --group`, `viper check`, ein alter Rechner daneben.

## 7 Offene Fragen

* Gruppen mit Rechnern **verschiedener Präfixe** (Rechnerdatei überschreibt `SERVER_PREFIX`): Die Servernamen bleiben verschieden, das Projekt ist das der Gruppe.
* Rechner einer Gruppe mit **verschiedenen Docker-Kontexten** (`SERVER_LOCATION` je Rechner): ein Fehler, ein Projekt gehört zu einem Docker-Host.

## 8 Befunde der Prüfung (13.11.2026)

Ein unabhängiger Prüfer hat den Entwurf gegen den Code und Docker Compose v5.7 / Engine 29.9.3 geprüft, mit Wegwerf-Projekten auf dem lokalen Docker. Bestätigt: `up --no-recreate <dienst>` lässt andere Dienste unberührt; `rm -sfv` nimmt das anonyme Volume des Sidecars mit; ein einzeln gestoppter Rechner startet den Sidecar nicht mit (`repairDocker` bleibt); zwei `up` verschiedener Dienste desselben Projekts laufen gleichzeitig. Eingearbeitet in diesen Entwurf: der Projektschlüssel mit Unterstrich (statt `g-`, der mit einem Rechner `g-web` kollidiert wäre); je Rechner eigene Schlüssel für Volumes, Secrets und `network_mode`/`depends_on` in der gemeinsamen Datei; kein `RemoveAll` auf das Verzeichnis der Gruppe; `Leftovers` zählt das Gruppenverzeichnis nicht als Rest; die Sperre nur um die Dateiänderung, damit `--parallel` parallel bleibt; atomares Schreiben; der Rückfall mit `docker rm -f -v`; `EnsureRunning` prüft den Dienst; Auffinden des Projekts ohne Container; zwei Wege im Code (alt und neu); der Umbau des Test-Fake als eigener Meilenstein.

## 9 Ergebnis (14.11.2026)

Gebaut in `internal/adapter/driven/compose/project.go` (Projekt, Auffinden, Lesen, Zusammenführen, atomares Schreiben), `file.go`, `server.go`; `ServerSpec.Group` kommt aus `Machine.Group`. Der Fake-Docker der Tests versteht `-p`, Dienstnamen und `rm`; `group_test.go` hat sieben Tests (gemeinsame Datei, Löschen eines Rechners, Rechner ohne Gruppe daneben, einzelner Start, gleichzeitiger Start von fünf Rechnern unter `-race`, Reste, ungültiger Gruppenname). Lauf gegen das lokale Docker (`COMPOSE_DOCKER=none`, Präfix `ctest`, Image `viper-machine:test`): `viper up --group` startet drei Rechner in `viper_g_cg` in 24 s, `viper check`, `viper ssh`, ein einzeln gestoppter und mit `viper up b` wieder gestarteter Rechner (die anderen unberührt), `viper down b` (die anderen laufen, `b` aus Datei, Schlüsseln und Volumes), `viper down --group` (Verzeichnis, Container, Volumes weg); dasselbe mit `NATS_ENABLED=true` (das Gruppennetz in der Datei, `check`: drei Rechner am Server, danach Netz und Reste weg). Nicht geprüft: `COMPOSE_DOCKER=dind` gegen echtes Docker (nur im Fake), ein Rechner im alten Layout neben einer Gruppe (nur im Fake: `EnsureRunning` und `DeleteServer` nehmen den Weg über das Label), Docker Desktop und entfernte Docker-Hosts.

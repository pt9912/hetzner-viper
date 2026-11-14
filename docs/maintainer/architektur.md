# Architektur von viper

`viper` ist nach dem Muster Ports und Adapter (hexagonale Architektur) gebaut. Die Regeln dafür prüft der Linter (`make lint`, Abschnitt `depguard` in `.golangci.yml`); sie sind keine Empfehlung.

## Schichten

```
cmd/viper/main.go          ruft nur app.Run auf
internal/
  model/                   Server, ServerSpec, Machine, Finding, Anbietertabelle (importiert nichts aus viper)
  port/
    driving/               Up, Down, Check, Preflight: was die CLI vom Programm verlangt
    driven/                cloud, dns, system, runlog, sshconfig, settings: was das Programm von außen braucht
  usecase/                 start (Up), stop (Down), check, preflight: die Abläufe
  adapter/
    driving/cli            Befehle, Parsen der Befehlszeile
    driven/                hetzner, aws, gcp, azure, cloudflare, nodns, system, editor, git, resolver,
                           runlog, sshconfig, settingsfile
  app/                     Composition Root: wählt die Adapter, baut die Use Cases, übergibt sie der CLI
```

Die Abhängigkeiten laufen nach innen: `model` kennt nichts, die Ports kennen nur das Modell, die Use Cases kennen Ports und Modell, die Adapter erfüllen Ports (driven) oder rufen sie auf (driving). Nur `app` kennt alles und steckt es zusammen. Unterschiede zwischen den Anbietern stehen ausschließlich in `internal/adapter/driven/<anbieter>`.

## Einen Anbieter hinzufügen

1. **Adapter** in `internal/adapter/driven/<anbieter>` schreiben. Er erfüllt `cloud.Provider` (`internal/port/driven/cloud`): `ServerByName`, `CreateServer`, `DeleteServer`, `EnsureLoginKey`, `EnsureFirewall`, `Validate` und die Namen für Meldungen. Die Logik arbeitet gegen eine eigene schmale `API`, die SDK-Schicht (`sdk.go`) ist dünn. So lässt sich die Logik ohne Konto testen.
2. **Anbietertabelle** in `internal/model/provider.go`: Name, Pflichteinstellungen (`Credentials`), geheime Umgebungsvariablen (`Secrets`) und die Standardwerte für Typ, Ort und Image.
3. **Verdrahtung** in `internal/app/provider.go` (`NewProvider`): Einstellungen des Rechners in die Optionen des Adapters übersetzen.
4. **Tests:** Fake der `API`, Tests der Logik; für einen echten Lauf einen Test mit Build-Tag `e2e`.
5. **Doku:** Handbuch (Kapitel 4), `.env.local.example`, `machines/example.env.local.example`.

Der Login-Schlüssel muss kein Objekt beim Anbieter sein: `LoginKey.Ref` ist der Name des Schlüssels oder der Schlüssel selbst (GCP, Azure).

## Container als Maschinen (Kubernetes, Docker Compose)

`PROVIDER=kubernetes` (`internal/adapter/driven/kubernetes`) macht aus einer Maschine ein StatefulSet mit einem Pod, einem Secret (Schlüssel) und einem PVC. Planung, Entscheidungen und Ergebnisse: `docs/plan/kubernetes.md`. Was es von den VM-Anbietern unterscheidet, und wo der Code es merkt:

* `ProviderInfo.Container` und `Machine.IsContainer()`: kein cloud-init (`start` fragt eine andere Probe, `check` überspringt cloud-init), kein `sudo`, keine Auslagerungsdatei, kein `code-server`, keine Firewall, kein DNS.
* `Server.ViaAPI` und `Server.SSHPort`: der Pod hat keine Adresse. `sshconfig.WriteConfig` schreibt einen `ProxyCommand` (`viper k8s-proxy`, Port-Forward mit `client-go` im Adapter, `Client.Proxy`); `ForwardAgent` bleibt aus. Alles danach geht weiter über `ssh -F <config> <alias>`.
* `ServerSpec.Bootstrap` trägt dieselben Schlüssel wie `UserData` (cloud-init) als Werte, für den Adapter.
* Das Image (`image/`, `viper image build` mit den eingebetteten Dateien aus `embed.go`, `make image`, `make image-test`, Workflow `image.yml` für ghcr.io; ein Test prüft, dass alles im Kontext steht, was das `Dockerfile` kopiert) läuft als uid 1000 ohne Rechte; Härtung von `sshd`, `tmux`- und VS-Code-Einstellungen nimmt `image/seed.py` aus `cloud-init.yaml`; die Versionen und Prüfsummen von `nats-server` und `nats` im `Dockerfile` hält ein Test gleich mit `internal/natsserver`.
* NATS: das Gruppennetz ist eine NetworkPolicy (Anlegen = Sperre), der Server wird über einen headless Service erreicht (Zertifikat auf den Dienstnamen), `nats-server` läuft als Prozess des Containers (`image/nats-supervisor.sh`, Neuladen per `SIGHUP` über die PID-Datei), die Konfiguration sagt `host: $POD_IP` (`natsserver.Config.Listen`) und trägt den Dienstnamen als `# viper-advertise`.


`PROVIDER=compose` (`internal/adapter/driven/compose`) macht aus einer Maschine einen Dienst eines Compose-Projekts auf dem lokalen Docker-Host: die Maschinen einer Gruppe teilen ein Projekt `viper_g_<gruppe>` mit einer `compose.yaml`, eine Maschine ohne Gruppe hat ihr eigenes (`project.go`: Projektschlüssel `g_<gruppe>` oder Servername, kein Unterstrich in Namen). `viper up` schreibt die Datei selbst (feste Typen, gleiche Eingabe gleiche Datei; für eine Gruppe liest es sie ein, setzt den Dienst der Maschine hinein und schreibt atomar, unter einer Sperre je Gruppe, die nur diese Änderung deckt), legt die Schlüssel als Dateien daneben (`secret/<server>/`) und startet nur die Dienste der Maschine mit `docker compose up -d --no-recreate --wait <dienste>`; `viper down` entfernt sie mit `rm --stop --force --volumes`, das Verzeichnis geht mit dem letzten Rechner. Das Projekt einer Maschine steht im Label `viper-project` (fehlt es: altes Layout, ein Projekt je Maschine, das mit dem alten Weg weiterläuft). Gruppenplan: `docs/plan/compose-gruppe.md`. Ein vorhandener Container wird nie neu angelegt; `cloud.Starter` (`EnsureRunning`) startet ihn und repariert den `dind`-Dienst nach einem Neustart allein. Der Zugang ist `viper docker-proxy` (`docker exec` mit `/dev/tcp`). Planung, Prüfungen und Ergebnisse: `docs/plan/compose.md`. Der Adapter ruft nur das Programm `docker` über `system.Runner` auf (kein SDK); Tests nutzen einen Fake des `docker`-Aufrufs, der Container und Volumes aus der erzeugten Datei nachbildet.

## NATS-Gruppen

Optional (`NATS_ENABLED`), Hetzner, AWS und Google Cloud (die Anbietertabelle `model/provider.go` führt das Feld `NATS`; der Plan für weitere Anbieter ist `docs/plan/nats-anbieter.md`). Bei AWS ist das „private Netzwerk“ eine Security Group `viper-nats-<gruppe>` im VPC des Subnetzes; ihr Anlegen ist die Sperre des ersten Starts. Bei Google Cloud ist es eine Firewall-Regel `viper-nats-<gruppe>` mit einem gleichnamigen Netzwerk-Tag; die Markierung steht in ihrer Beschreibung (Regeln haben keine Labels), die VMs müssen in einer Zone liegen. `Server.InNetwork()` (nicht `PrivateIP`) sagt, ob ein Server angebunden ist. Der Plan mit allen Entscheidungen und den Ergebnissen der Tests gegen Hetzner steht in `docs/plan/nats.md`; hier die Bausteine.

| Baustein | Aufgabe |
|---|---|
| `model/nats.go` | Rolle, Zustand, Metadaten, Labels (`viper-managed`, `viper-nats-group`, `-role`, `-state`), Namensprüfung, `PrivateNetwork` |
| `port/driven/cloud/nats.go` | `NATSProvider`: eine **optionale** Fähigkeit neben `Provider` (Gruppe auflisten, Labels, Netzwerkzone, privates Netzwerk anlegen/finden/anbinden/löschen). `cloud.AsNATS` findet sie auch hinter dem Wrapper `app.Provider` (`Unwrap`) |
| `adapter/driven/hetzner/nats.go`, `network.go` | Umsetzung mit Label-Selektoren und Hetzner Networks |
| `natsserver` | Reiner Text: gepinntes Release von `nats-server` und `nats` (Version, SHA-256), `server.conf`, systemd-Unit, NKey-Erzeugung, `connection.env` |
| `usecase/natsgroup` | SSH-Operationen, die `up` und `down` teilen: andere Maschine der Gruppe erreichen, Clientliste ändern (`Ops.Update`) |
| `usecase/start/nats.go` + `scripts/nats*.sh` | Rolle bestimmen, Server bootstrappen, Client registrieren |
| `usecase/stop/nats.go` | Widerruf, Schutz des Servers, Netzwerk aufräumen |
| `usecase/check/natschecks.go` + `natsstatus.sh` | Prüfungen aus der Sicht des Anbieters, der Maschine und von außen |
| `servertest/nats.go` | Fake der Fähigkeit für die Use-Case-Tests |

Verhalten, das man nicht aus dem Code erraten sollte:

* **Erststart-Sperre:** Das Netzwerk `viper-nats-<gruppe>` wird angelegt, ohne vorher zu suchen. Hetzner erlaubt den Namen nur einmal (auch bei gleichzeitigen Aufrufen), also wird genau einer der Server-Kandidat. Ein Netzwerk ohne Server, das jünger als 15 Minuten ist, gilt als „Server wird noch angelegt“ (`firstStartWindow`); die Clients legen ihre Maschinen an und warten. Ein älteres Netzwerk ohne Server ist ein unterbrochener Start und wird abgelehnt. Eine automatische Übernahme der Serverrolle gibt es nicht (Labels haben kein Compare-and-Set).
* **Clientliste:** Die Datei `~/.viper-nats/server.conf` trägt oben die Zeilen `# viper-client <maschine> <schlüssel>`; daraus liest `ParseClients` die Liste zurück. `Ops.Update` liest die Datei mit ihrem Hash, ändert die Liste und schreibt nur zurück, wenn der Hash unter `flock` noch stimmt (Skript beendet sich sonst mit Status 3, „changed meanwhile“); dann liest es neu. Bevor die Datei ersetzt wird, prüft `nats-server -t` sie.
* **Skripte laufen als `bash -c '<skript>' nats <argumente>`**, weil stdin die Daten trägt (Konfiguration, Schlüssel). Verbindungsfehler von `ssh` werden bis zu 12 Mal wiederholt (`lostConnection`); alle Skripte sind wiederholbar. Zeilen, die mit `nats` beginnen, zeigt `up` dem Nutzer.
* **Der Seed** entsteht im Speicher von `viper` (`nkeys`), geht per SSH-stdin auf die Maschine (`natsidentity.sh`, nur wenn dort noch keiner liegt) und wird lokal weder gespeichert noch protokolliert.
* **Zertifikat:** selbstsigniert, 31 Tage, keine CA, keine Erneuerung (Gruppen laufen kurz); die Clients vertrauen genau diesem Zertifikat. Läuft es ab oder ändert sich die Adresse, endet die Gruppe. Es ist ein **Blattzertifikat** (`basicConstraints=critical,CA:FALSE`, `keyUsage=digitalSignature`, `extendedKeyUsage=serverAuth`, SAN mit der IP): `openssl req -x509` markiert sonst `CA:TRUE`, und rustls (jeder Rust-Client, etwa Nuntius) lehnt das als Serverzertifikat mit `CaUsedAsEndEntity` ab, Go-Clients nicht. Zertifikate von Gruppen, die vor v0.16 angelegt wurden, sind noch CA-Zertifikate; sie werden nicht ersetzt (keine Erneuerung).
* **Keine `allow_responses`:** Die Option erlaubt, auf das Reply-Subject jeder zugestellten Nachricht zu antworten, ohne die Sendeliste zu prüfen. Weil jede Maschine `viper.<g>.>` liest und das Reply-Feld frei wählbar ist, könnte jede Maschine auf jedes Subject der Gruppe senden (mit `nats-server` 2.15.2 nachgewiesen; v0.15.0 hatte sie noch). `Render` schreibt sie deshalb nicht; Antworten sind normale Nachrichten im eigenen Teilbaum. `internal/natsserver/forge_test.go` startet `nats-server` als Bibliothek mit den echten Rechten und versucht vier Fälschungswege; der Test schlägt an, wenn die Option zurückkehrt. Er ist der Grund für die Testabhängigkeiten `nats-server/v2` und `nats.go` in `go.mod`.
* **MCP-Server (`NATS_MCP`):** `start/natsmcp.go` läuft in `provision()` nach `claudeLogin` (Claude Code ist dann installiert). Die Binary kommt aus `internal/mcpbin` (je Architektur der Maschine) oder aus `NATS_MCP_BINARY`, geht per SSH-stdin nach `~/.viper-nats/viper-nats-mcp.new` und wird erst nach Prüfung der SHA-256 verschoben (`natsmcpinstall.sh`). `mcp.env` (600) trägt `VIPER_NATS_MCP_PUSH_FROM` und `VIPER_NATS_MCP_TOPICS` (Standard-Topics, beim Start abonniert, nicht abbestellbar). `natsmcpclaude.sh` ersetzt den Eintrag (`claude mcp remove`/`add --scope user nats`, `PATH` mit `~/.local/bin`, weil `ssh` ohne Login-Shell läuft) und setzt mit `jq` genau die `mcp__nats__*`-Einträge in `permissions.allow` (andere bleiben; `off` entfernt alte). `natsmcptest.sh` startet den Server wie Claude Code über stdio (`initialize`, `tools/list`). Ohne `jq` und mit Erlaubnisliste ist das nur eine Warnung. `viper check` liest die Zeilen `mcp_*` aus `natsstatus.sh`. `viper` startet Claude nie mit dem Channel-Schalter.
* **Labels entscheiden**, nicht `NATS_ENABLED`: `down` und `check` behandeln eine Maschine mit NATS-Labels als Gruppenmitglied.
* **Falle `nats rtt`:** Das Programm beendet sich auch bei abgelehntem Login oder gestopptem Server mit Status 0. Die Verbindungstests (`natsconnect.sh`, `natsstatus.sh`) veröffentlichen deshalb eine Nachricht im eigenen Bereich (`nats pub`), das mit Status 1 endet.
* **Neue Release-Version** von `nats-server` oder `nats`: Version und SHA-256 aus der `SHA256SUMS` des Releases in `natsserver.go` eintragen (`Version`, `checksums`, `CLIVersion`, `cliChecksums`) und einen echten Lauf machen (der Download wird gegen die Summe geprüft).

Getestet wurde das Zusammenspiel mit echten Hetzner-Servern (mehrere Maschinen gleichzeitig, Wiederanlauf nach einem Fehler, `down` mit Widerruf, `check` bei gestopptem Dienst); die Unit-Tests laufen gegen Fakes. Ein Lauf kostet wenige Cent: Testprojekt, eigenes `SERVER_PREFIX`, `SERVER_TYPE=cpx22` (`cax11` war zeitweise nicht bestellbar), danach Server, Netzwerk `viper-nats-<gruppe>`, Firewall `<prefix>-ssh` und Schlüssel `<prefix>-login` löschen.

## Gruppen (`groups/`)

Der Plan mit Entscheidungen und Review-Ergebnissen steht in `docs/plan/gruppen.md`; hier die Bausteine.

* **Laden:** `settingsfile/group.go` liest `groups/*.env`. `memberOf` findet die Gruppe eines Rechners (doppelte Zugehörigkeit ist ein Fehler; eine unlesbare Datei einer fremden Gruppe nur eine Warnung in `Machine.Warnings`, außer ihr Text nennt den Rechner). `Load` und `Prefix` setzen die Einstellungen der Gruppe zwischen `.env.local` und die Datei des Rechners und `NATS_GROUP` auf den Dateinamen. `LoadGroup` (Port `settings.Store`) liefert `model.Group` für die Gruppenbefehle.
* **Ausführung:** `usecase/groupops.Runner` (Port `driving.GroupRunner`) führt Aufgaben je Rechner gleichzeitig aus, höchstens `--parallel` (Standard 8), und nimmt die Plätze in der Reihenfolge der Aufgaben, sodass die ersten Rechner zuerst starten. Das Ergebnis je Rechner (`GroupResult`), die Tabelle und der Exit-Status (`GroupExitCode`: 0, 1, 130) liegen im Port.
* **Befehle:** `cli/group.go` (`runGroup` für `check` und `ssh --group`), `cli/groupup.go`, `cli/groupdown.go`. `prefixWriter` setzt Teilzeilen zusammen und schreibt unter einem Mutex je Terminal. `groupWriters.task` macht aus einem Lauf eine Aufgabe.
* **Start:** `groupPlan` bestimmt, wer auf wen wartet: der erste Rechner mit NATS (der Server) auf niemanden, alle anderen mit NATS auf ihn, und je (Schlüssel, Firewall) wartet der Rest auf den ersten Rechner, der sie anlegt. Über `driving.UpOptions` meldet `start` mit `OnServerCreated`, dass der Server samt Rolle und Netz da ist (bei einem vorhandenen Server ohne Gruppe erst nach `natsSetup`); `SkipPreflight` (die Prüfung lief für alle vorab) und `ServerWait` (15 Minuten) gehören dazu. Ein `gate` je Rechner öffnet sich mit Erfolg oder ohne; scheitert der Server, werden die laufenden Clients über einen Kontext je Client abgebrochen.
* **Stopp:** zwei Wellen (alle außer dem Server, dann der Server). Die Rolle liest `stop.Service.GroupRole` (Port `driving.GroupRoler`) aus den Labels. Die Fragen beantwortet ein gemeinsamer Leser (`newAsker`) hinter einem Mutex (`groupAsker`); die Aufforderung trägt den Namen des Rechners. Scheitert einer der ersten, bleibt der Server.
* **Gemeinsamer Zustand:** `natsconfig.sh` schreibt in eine eigene Datei je Aufruf (vorher `server.conf.new` für alle, vor dem `flock`: gleichzeitig beitretende Rechner überschrieben sich); `Ops.Update` versucht 12 Mal mit zufälliger Pause (`Ops.Jitter`); `sshconfig` schreibt Konfiguration, Pin und ID atomar (temporäre Datei, Rename); `EnsureLoginKey` und `EnsureFirewall` nehmen das, was ein gleichzeitiger Lauf angelegt hat.

## Tests

* `make test` führt alles mit `-race` aus und braucht kein Konto: die Use Cases laufen gegen Fakes (`internal/servertest`), die Adapter gegen nachgebildete APIs.
* Echte Läufe haben den Build-Tag `e2e` und laufen nur mit einer Umgebungsvariablen:
  * `internal/adapter/driven/aws/e2e_test.go`: Schlüsselpaar, Security Group und eine kleine Instanz, danach alles löschen (`VIPER_E2E_AWS=<profil>`).
  * `internal/app/e2e_test.go`: vollständiger Lauf von `up` und `down` mit `DNS_PROVIDER=none`, auch auf ARM (`VIPER_E2E_TYPE=t4g.small`).

  Beide kosten wenige Cent und räumen auch bei einem Fehler auf. Die Anmeldung kommt aus `~/.aws`; im Container muss das Verzeichnis schreibbar sein, damit das SSO-Token erneuert werden kann.
* Die Rechnerdatei `machines/aws-test.env.local` hat dieselben Einstellungen wie der Lauf, für einen Versuch von Hand.
* Die Quellen von `viper find` prüft jede Nacht der Workflow `price sources` (Build-Tag `live`, siehe unten).

## Preisquellen von find

`viper find` liest seine Preise aus öffentlichen Quellen, ohne Konto. Zwei davon sind Webseiten, keine Schnittstelle, und ändern sich ohne Vorwarnung:

| Anbieter | Quelle | Auswertung |
|---|---|---|
| Hetzner | die Seiten unter `hetzner.com/cloud/…` (Ausstattung) und die Datei `cloud_data.json`, die sie laden (Preise je Standort, IPv4) | `internal/adapter/driven/hetzner/public.go`: Text der Seiten mit einem Muster, die Datei als JSON |
| Google Cloud | die Preisseiten der Familien, die Seite der Platten, die Netzwerk-Preisseite | `internal/adapter/driven/gcp/web.go`: Muster über den Datenblock der Seite (Regionsetiketten, Textzellen, Preise), Tabellen sind die Zellen zwischen zwei Etiketten |
| Azure | Daten des Preisrechners (Kerne, RAM) und die Retail Prices API (Preise) | `internal/adapter/driven/azure/catalog.go` |
| AWS | öffentliche Preisdatei je Region (EC2 und Netzwerkdienst) | `internal/adapter/driven/aws/catalog.go`, als Strom gelesen |
| EZB | tägliche Referenzkurse | `internal/adapter/driven/ecb/ecb.go` |

Die Regeln dabei: eine Zeile, die nicht passt, fehlt, sie wird nie geraten; eine Seite, die gar nichts liefert, ist ein Fehler (`did it change?`) und wird nicht gemerkt; das Ergebnis liegt einen Tag im Cache (`internal/adapter/driven/pricecache`), und ein alter Stand gilt ohne Netz höchstens sieben Tage.

### Nächtliche Prüfung

Der Workflow `.github/workflows/sources.yml` (`price sources`) führt jede Nacht um 03:17 UTC die Tests in `internal/live/` aus (Build-Tag `live`; von Hand: `go test -tags live -count=1 -v ./internal/live/`, rund eine Minute). Sie lesen die echten Quellen und prüfen die Form (genug Typen, bekannte Typen darunter, Preise über null, die IPv4-Gebühr), nicht die Preise.

* **Quelle nicht erreichbar** (Netz, Zeitlimit, abgeschnittene Antwort, `403`, `429`, `5xx`; `internal/livecheck`): Der Test überspringt sich. Das sagt nichts über `viper`, und es gibt kein Issue. Eine leere Antwort (`EOF`) zählt nicht dazu: So sieht auch eine geänderte Quelle aus. Eine Quelle, die jede Nacht übersprungen wird (etwa weil sie die Rechner von GitHub abweist), fällt nicht auf Rot, steht aber als Warnung auf der Seite des Laufs („source not reached“).
* **Quelle antwortet, wird aber nicht mehr gelesen:** Der Test schlägt fehl (`--- FAIL` im Protokoll), und der Workflow legt ein Issue „viper find: Preisquellen lassen sich nicht mehr auswerten“ an (oder kommentiert das offene) mit den Zeilen des Laufs. Die nächste Nacht, in der alle Quellen wieder gelesen werden und keine übersprungen ist, schließt es.

Bricht der Lauf ohne einen fehlgeschlagenen Test ab (Aufbaufehler, fehlendes Modul, Zeitlimit), wird er rot, aber es gibt kein Issue; die Tests laufen gleichzeitig, damit eine langsame Quelle die anderen nicht die Zeit kostet. Die Prüfungen verlassen sich auf Familien (`cpx`, `ccx`, `e2-`, `n2-`) und nicht auf einzelne Typen, die Hetzner oder Google auch ohne Änderung der Seite ausmustern.

Bei einem Issue: Die Seite im Browser ansehen, das Muster in der Auswertung anpassen (es steht in der Tabelle oben), den Test von Hand laufen lassen und einen Test mit der neuen Form der Seite ergänzen. Geplante Workflows laufen nur auf dem Standard-Branch, und GitHub schaltet sie nach 60 Tagen ohne Aktivität im Repository ab.

## Stand der Anbieter

| Anbieter | Getestet |
|---|---|
| AWS | echte Läufe: Adapter, `up` und `down`, x86 und ARM, ohne Cloudflare |
| Hetzner | Server anlegen und löschen gegen nachgebildete Schnittstellen (die früheren Bash-Skripte liefen echt); NATS-Gruppen (Netzwerke, Labels, Bootstrap, Clients, `down`, `check`) auch in echten Läufen |
| Docker Compose | echter Lauf auf dem lokalen Docker-Host: `up`, `check`, `ssh`, Docker im Rechner (dind), Neustart und Reparatur, `up` auf vorhandener Maschine, `down`, Reste; NATS-Gruppe aus drei Containern (Netz, Name im Zertifikat, Zustand als Volumes, Wechsel der Adresse); Docker Desktop und entfernte Hosts nicht getestet |
| Kubernetes | echte Läufe in k3d: Adapter, `up`, `check`, `ssh`, Docker (dind), Ersetzen des Pods, `down`, NATS-Gruppe aus drei Pods mit `viper nats` und Betreiber, NetworkPolicy; EKS, GKE, AKS nicht in einem Konto getestet |
| Google Cloud, Azure | nur gegen nachgebildete Schnittstellen; die SDK-Schicht ist vom Compiler geprüft, nicht gegen ein Konto gelaufen |

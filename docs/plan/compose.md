# Plan: Docker Compose als Anbieter (Rechner als Container auf einem Docker-Host)

**Ziel:** `PROVIDER=compose` richtet einen „Rechner“ als **Container** auf einem Docker-Host ein: dem eigenen Rechner oder einem entfernten (Docker-Kontext). `viper up` **erzeugt dazu die `compose.yaml`** (Dienste, Netze, Volumes, Secrets) und startet sie mit `docker compose up`; `viper down` beendet sie wieder. Wie bei Kubernetes braucht es kein Cloud-Konto, aber auch keinen Cluster: nur Docker.

**Stand:** Entwurf, einmal unabhängig geprüft (mit Versuchen auf dem lokalen Docker-Host); die Befunde sind eingearbeitet (§13) und ändern den Plan bei den Secrets, den Labels, den Netzen, dem Docker-Sidecar und dem Zugang. Nichts ist umgesetzt.

**Kennzeichnung:** *[V]* heißt, im Code, in einem Test oder in einem Lauf bestätigt. *[?]* heißt Vermutung; jede solche Stelle wird in Meilenstein 0 geprüft, bevor etwas darauf gebaut wird. **(zu bestätigen)** markiert Annahmen, die eine Entscheidung brauchen (§9).

Grundlage ist [`kubernetes.md`](kubernetes.md): Das Image `viper-machine`, die Anbindung von Containern in `start`, `stop` und `check` und die NATS-Gruppen im Container gibt es schon. Dieser Plan ändert daran wenig und ergänzt einen zweiten Anbieter für Container.

## 1 Ausgangslage

Was schon da ist *[V, Plan und Code von `kubernetes.md`]*:

* **Das Image** `viper-machine` (`image/`): Benutzer `builder` ohne root und ohne `sudo`, `sshd` auf Port 2222, Host-Schlüssel samt Zertifikat und erlaubte Schlüssel als Dateien unter `/run/viper/secret`, Homebrew und Heimverzeichnis, die beim ersten Start auf ein Volume gelegt werden, Docker-Client mit Compose, `nats-server` und `nats`. `viper image build` baut es mit Docker; `image/test.sh` prüft es mit `docker run` *[V]*, ohne Cluster.
* **Container-Fähigkeit im Code:** `ProviderInfo.Container`, `Machine.IsContainer()`, `Server.ViaAPI` und `SSHPort`. `start`, `stop` und `check` kommen damit ohne Adresse, DNS, Firewall, `cloud-init`, `sudo` und Auslagerungsdatei aus *[V]*. Der Zugang geht über einen `ProxyCommand` der SSH-Konfiguration, der ein Programm von `viper` aufruft (`viper k8s-proxy`).
* **NATS im Container:** `nats-server` als Prozess des Containers (`image/nats-supervisor.sh`), `host: $POD_IP`, Zertifikat auf einen Namen, Reload per Signal *[V]*.
* **`cloud.Leftovers`:** `viper down` räumt Reste eines Rechners beim Anbieter auf *[V]*.
* **Docker 29.9.3 und Compose v5.7.0 sind in dieser Umgebung da** *[V]*; ein Netz mit festem Namen lässt sich nur einmal anlegen: sechs gleichzeitige `docker network create` ergaben ein „angelegt“ und fünf „already exists“ *[V, geprüft für diesen Plan]*. Das ist die Sperre für den ersten Start einer NATS-Gruppe, so sauber wie die NetworkPolicy bei Kubernetes.

## 2 Grundregeln

1. **`viper` besitzt, was es erzeugt.** Eine `compose.yaml` je Projekt liegt im Zustandsverzeichnis (`~/.local/state/hetzner-claude/compose/<projekt>/`), wird bei jedem `viper up` neu geschrieben und ist nicht zum Bearbeiten da. Eigene Dienste kommen über eine zweite Datei (§9, D5), die `viper` nur einbindet.
2. **Alles trägt das Label `viper-managed=true`** (Dienste, Netze, Volumes); `viper` ändert und löscht nur, was es trägt (wie bei Kubernetes).
3. **Keine Geheimnisse in der `compose.yaml` oder in der Umgebung.** Schlüssel liegen als Dateien (Modus 0600) im Zustandsverzeichnis und kommen als Compose-`secrets` in den Container.
4. **Der Docker-Host gehört dem Nutzer.** Der Anbieter wählt ihn nur über den **Docker-Kontext** `SERVER_LOCATION` (ohne Standard, wie der Kontext bei Kubernetes) und nimmt nie den „aktuellen“.
5. **Ehrlich zur Qualität:** Auf dem lokalen Docker-Host ist alles prüfbar und wird geprüft; ein entfernter Host (`ssh://`) nutzt dieselben Befehle, ist aber nicht einzeln getestet.

## 3 Abbildung auf Docker Compose

| `viper` | Docker Compose |
|---|---|
| Anbieter | `PROVIDER=compose`; `SERVER_LOCATION` ist der Docker-Kontext (`default` für den lokalen) |
| Server | ein **Dienst** der Compose-Datei, Containername `<SERVER_PREFIX>-<maschine>` |
| Projekt | eine Maschine = ein Projekt `viper-<präfix>-<maschine>`; eine **Gruppe** (`groups/<gruppe>.env`) = ein Projekt `viper-<präfix>-<gruppe>` mit einem Dienst je Maschine *[(zu bestätigen) D2]* |
| `Server.ID` | die ID des Containers (ändert sich beim Neuanlegen, wie die UID beim StatefulSet) |
| Login-Schlüssel | der Schlüssel selbst als Referenz (kein Schlüsselobjekt) |
| Host-Schlüssel, erlaubte Schlüssel | Compose-`secrets` in der **langen Schreibweise** mit absolutem `target:` (`/run/viper/secret/host_key` usw.; die kurze legt sie unter `/run/secrets/<name>` ab, das Einstiegsprogramm findet sie dann nicht). Es sind **Bind-Mounts der Datei des Hosts**: `uid`, `gid` und `mode` der Compose-Datei gelten nicht, Besitzer und Modus der Datei auf dem Host zählen. `viper` schreibt sie in ein Verzeichnis 0700 mit Dateien, die Benutzer 1000 lesen kann (Besitzer uid 1000, oder 0644 im 0700-Verzeichnis) *[V, geprüft: eine Datei root:root 0600 ist im Container nicht lesbar]*. **Nur lokaler Docker-Host in Version 1** (§7.7) |
| Firewall, DNS, Adresse | keine (`DNS_PROVIDER=none` ist Pflicht) |
| Größe (`SERVER_TYPE`) | `<cpu>x<GiB>` → `cpus` und `mem_limit` des Dienstes (beide gültig in Compose v5.7 *[V]*; nicht zusammen mit `deploy.resources`, das ist ein Fehler) |
| Image (`SERVER_IMAGE` bei diesem Anbieter) | `COMPOSE_IMAGE` (Pflicht, ohne Standard; `viper image build` baut es **lokal**, kein Import in einen Cluster nötig); `pull_policy: never`, denn ein lokal gebautes Image gibt es in keinem Register, und Compose würde sonst versuchen, es zu ziehen |
| Platte | zwei **benannte Volumes** `<name>-home` und `<name>-brew` (Heimverzeichnis und Homebrew). Docker füllt ein leeres benanntes Volume aus dem Image, mit Besitzer 1000; die Markierung `.viper-seeded` gibt es im Image nicht, das Einstiegsprogramm legt also wie gewohnt Homebrew auf das Volume *[V, geprüft]*. Der Fall „Wurzel des Volumes gehört root“ aus `entry.sh` tritt hier nicht auf (nur bei Bind-Mounts) |
| Docker auf dem Rechner | ein zweiter Dienst `<name>-docker` (`docker:dind`, `privileged`) mit `network_mode: service:<maschine>`, damit `127.0.0.1:2375` im Rechner der Daemon ist; nur auf Loopback gebunden *[V, geprüft]*. `healthcheck` am Daemon und `depends_on: condition: service_healthy`, damit das Warten von `viper` deterministisch ist. **Wird der Rechner-Container allein neu gestartet** (Absturz, Neustart des Hosts), bleibt der Daemon im alten Netzwerk-Namensraum und ist für den Rechner weg *[V, geprüft]*: `depends_on` mit `restart: true` (nicht geprüft) oder `viper check`/`viper up` starten das Projekt neu (§7.8). Für `/var/lib/docker` gibt es kein Volume: die Images im Daemon gehen beim Neuanlegen verloren (bei Kubernetes ebenso). Alternativ `remote` oder `none` (wie `K8S_DOCKER`) *[(zu bestätigen) D4]* |
| Löschen | `docker compose -p … down` (eine Maschine = ein Projekt) und danach `docker volume rm` der beiden Volumes (`down -v` entfernt benannte Volumes nur des Projekts, `rm -v` nur anonyme); das Gruppennetz entfernt `viper` selbst, wenn das letzte Mitglied weg ist |

## 4 Der Zugang

Der Container ist über SSH erreichbar wie ein Pod. Zwei Wege:

* **A: `ProxyCommand` mit `docker exec`.** `viper docker-proxy <maschine>` (ein Unterbefehl wie `viper k8s-proxy`, ohne Shell-Quoting im SSH-Konfigurationstext) startet `docker --context … exec -i <container> …` und verbindet Standard-Ein-/Ausgabe mit `127.0.0.1:2222` im Container. Dafür braucht der Container ein Programm, das das kann: `bash` (`/dev/tcp`) oder `nc`. Das Image hat `bash`, `nc` nicht *[V]*. Geprüft: `docker exec -i <c> bash -c 'exec 3<>/dev/tcp/127.0.0.1/2222; cat <&3 & cat >&3'` als `ProxyCommand` trägt eine echte SSH-Sitzung, 20 MB hochgeladen mit stimmender Prüfsumme, und endet bei normalem Ende sauber ohne Reste *[V]*. **Bricht aber der Client hart ab** (`kill -9` von `ssh` oder `docker exec`), bleiben `bash` und `cat` im Container stehen (`docker exec` beendet sie nicht) und sammeln sich *[V]*. Abhilfe: `wait -n; kill $!` im Einzeiler (löst das Halbschließen), und `viper check` beseitigt verwaiste Prozesse (oder ein `timeout` im Einzeiler) *[?: M0]*. Funktioniert mit einem entfernten Kontext, solange nur der Zugang gemeint ist (die Secrets nicht, §7.7).
* **B: Port auf `127.0.0.1` des Docker-Hosts.** Einfach, aber bei einem entfernten Host öffnet es einen Port dort, und es kollidiert bei mehreren Maschinen (Portverwaltung).

Empfehlung **A**: wie bei Kubernetes, keine Ports, kein Host-Zustand *[(zu bestätigen) D3]*.

## 5 NATS-Gruppen

* **Netz der Gruppe:** ein Netz `viper-nats-<gruppe>`, das **`viper` selbst mit `docker network create` anlegt** (das ist die Sperre des ersten Starts *[V, §1]*) und das die Compose-Dateien als `external: true` einbinden, zusätzlich zum Standardnetz des Projekts für den Zugang ins Internet. Ein `external`-Netz, das fehlt, ist ein klarer Fehler. Ein Netz mit festem `name:`, das Compose selbst anlegt, gehört keinem Projekt, wird von einem zweiten still mitbenutzt und bleibt nach dem letzten `down` stehen *[V, geprüft]*; deshalb legt und löscht `viper` es selbst, das Löschen beim letzten Mitglied (Leftovers und `down`; die Prüfung „letztes Mitglied“ ist bei gleichzeitigem `down` ein Wettlauf, den das Löschen des Netzes selbst entscheidet: es scheitert, solange jemand daran hängt). Anders als bei Kubernetes gibt es keine Policy, die ein Plugin durchsetzen muss: Ein Container, der nicht am Netz hängt, erreicht den Port nicht.
* **Adresse des Servers:** der Dienstname im Netz (`viper-nats-<gruppe>`, ein Netz-Alias am Server-Dienst); das Zertifikat lautet darauf, der Server lauscht auf der Adresse seines Containers (`$POD_IP`-Gegenstück, siehe unten). Damit gilt dasselbe Modell wie bei Kubernetes: Name im Zertifikat, Adresse darf wechseln.
* **Anpassungen am Code:** Die Variable heißt bei Kubernetes `POD_IP`; ein Container hat keine Downward-API. `hostname -i` ist mit zwei Netzen falsch (die Reihenfolge der Adressen folgt keinem Netz) *[V, geprüft]*. `viper` gibt deshalb das **Teilnetz des Gruppennetzes** (aus `docker network inspect`) als Umgebungsvariable mit, und `entry.sh` wählt daraus die eigene Adresse (`/proc/net/fib_trie`). `natsserver.ListenPodIP` bleibt als Name oder wird neutral umbenannt *[(zu bestätigen)]*; `NATSServiceHost` und `Endpoint.Listen` brauchen einen Namen ohne Namespace.
* **Rolle, Gruppe, Zustand:** Docker kann die Labels eines laufenden Containers **nicht ändern** (`docker update` hat keine Label-Option), und ein Neuanlegen würde den laufenden NATS-Server beenden *[V, geprüft]*. Rolle und Gruppe stehen bei der Anlage als Labels am Container (sie ändern sich nie). Der **Zustand** (`provisioning`, `ready`, `failed`) steht als **kleines Objekt für sich**: je Maschine ein Volume/Netz `viper-nats-<gruppe>-<maschine>-<zustand>` mit Label; ein Wechsel ist Anlegen des neuen und Löschen des alten (die Namenseindeutigkeit macht das atomar, wie die Sperre) *[(zu bestätigen) D7]*. Alternative: Zustand im Server-Container ablegen und mit `docker exec cat` lesen. Das ist der unsicherste Punkt dieses Plans.

## 6 Änderungen am vorhandenen Code

1. **Neuer Adapter** `internal/adapter/driven/compose`: ruft das `docker`-Programm auf (über `system.Runner`, wie die SSH-Aufrufe; kein Docker-SDK), erzeugt die Compose-Datei mit `gopkg.in/yaml.v3` (schon in `go.mod`) und implementiert `cloud.Provider` und `cloud.NATSProvider` und `cloud.Leftovers`.
2. **`ProviderInfo`** für `compose` (`Container: true`, NATS) und die Einstellungen `COMPOSE_*` in `settingsfile`; Prüfungen wie bei `K8S_*` (`DNS_PROVIDER=none`, `WORKSPACE_DIR` unter `/home/builder/`, kein `VIPER_SWAP`/`VIPER_CODE_SERVER`).
3. **SSH-Konfiguration:** `sshconfig.WriteConfig` schreibt für diesen Anbieter `viper docker-proxy`; heute fest `k8s-proxy` und Port 2222 *[V]*. Der Anbieter wird an `Machine` erkannt.
4. **`viper docker-proxy`** neben `viper k8s-proxy` (Port `driving.Proxy`, Anbindung in `app`).
5. **Preflight (`Validate`):** `docker` erreichbar, Kontext da, Compose-Plugin da, Image vorhanden (`docker image inspect`), Rechte (Zugriff auf den Docker-Socket), Platz; eine Warnung für `dind` (privilegierter Container, gibt Root auf dem Host).
6. **`viper image build`** arbeitet ohne `--k3d` und ohne Register: das Image liegt danach im lokalen Docker-Host und wird direkt genutzt *[V: es ist ein lokales `docker build`]*. Einen Kontext (`--context`) gibt der Code heute nicht weiter (§7.7: Version 1 nur lokal).
7. **Stellen, die heute Kubernetes voraussetzen**, wenn `IsContainer()` gilt, und sich ändern müssen *[V, aus dem Code]*: `sshconfig.go` (`Port 2222`, `k8s-proxy`, `HostName`; `IsContainer()` kennt den Anbieter nicht: ein Feld oder eine Methode für „welcher Container-Anbieter“), `start/nats.go` (`NATSServiceHost(group, K8S_NAMESPACE)`), `start/steps.go` und `provision.go` (`K8S_DOCKER`, ein `kubectl`-Hinweis), `stop/stop.go` („pod, K8S_NAMESPACE“), `check/check.go` („a pod“), `cli/cli.go` und `native.go` (`k8s-proxy`, die Meldung „Kubernetes cluster“), `app/provider.go` (`case "kubernetes"`, `Proxy`), `settingsfile` (`COMPOSE_*`). `cloud.Leftovers` liefert hier das Gruppennetz, die Volumes und die Dateien im Zustandsverzeichnis.
8. **Namen und Texte:** Namen von Maschine und Gruppe sind durch `nameRe` und `natsNameRe` begrenzt (sicher für YAML und Projektnamen); `COMPOSE_IMAGE` und `SERVER_LOCATION` sind freier Text: nur über `yaml.v3` schreiben und den Kontext nur als `--context <wert>` weitergeben, nach Prüfung, dass er nicht mit `-` beginnt.

## 7 Offene Fragen und Risiken

1. **Zustand von Labels (NATS)** wie in §5: eigener Meilenstein 0.
2. **Eine Datei, mehrere Maschinen:** `viper up web` und `viper up api` unabhängig voneinander laufen lassen, während beide in eine Gruppen-Datei schreiben wollen (Gruppe = ein Projekt): gleichzeitiges Schreiben und `docker compose up` desselben Projekts von zwei Prozessen. Bei einer Maschine je Projekt (D2, Empfehlung) fällt es weg, und die Gruppe verbindet nur das **Netz** (`viper-nats-<gruppe>`), nicht ein Projekt.
3. **Privilegierter `dind`-Dienst** gibt Root auf dem Docker-Host (bei Kubernetes: auf dem Knoten). Auf dem eigenen Rechner ist das der Nutzer selbst, auf einem geteilten Host nicht; das Handbuch sagt es.
4. **Docker Desktop (Windows, macOS):** Der Docker-Host ist eine VM; Verhalten von `--privileged`, Volumes und Netzen ist dort nicht einzeln geprüft.
5. **Rootless Docker/Podman als Host:** `--privileged` für `dind` geht nicht; dann `remote` oder `none`.
6. **Verbindungsabbruch des Transports:** siehe §4 (harter Abbruch hinterlässt Prozesse im Container).
7. **Entfernter Docker-Host:** Datei-Secrets sind Bind-Mounts eines Pfads **auf dem Host des Daemons**; bei `ssh://` zeigt der Pfad auf das Dateisystem dort, Docker legt fehlende Quellen als leere Verzeichnisse an *[nach Doku, nicht getestet]*. Version 1 unterstützt deshalb **nur einen lokalen Daemon** (`default`, ein lokaler Socket); ein entfernter Host braucht einen anderen Weg für die Schlüssel (Hochladen nach dem Anlegen, ein einmaliges Volume) und wird erst danach geplant.
8. **Neustart des Rechner-Containers allein** (Absturz, Neustart des Hosts) trennt den Docker-Dienst (`network_mode: service:`), siehe §3. `restart: true` bei `depends_on` ist die erste Wahl, wenn es wirkt *[?: M0]*; sonst startet `viper check` bzw. `viper up` das Projekt neu.
9. **Gleichzeitiges `docker compose up` desselben Projekts** (zwei `viper up` für dieselbe Maschine) scheitert mit „Conflict. The container name … is already in use“ *[V, geprüft]*: ein Dateisperre je Projekt im Zustandsverzeichnis für `up` und `down`.

## 8 Bewusst nicht enthalten

* Docker Swarm, `docker stack`, mehrere Docker-Hosts für eine Gruppe.
* Eigene Compose-Dateien des Nutzers **verwalten** (nur einbinden, D5).
* Ein veröffentlichtes Image (wie bei Kubernetes: `viper image build`).
* Preise in `viper find`.

## 9 Entscheidungen

D1 **Anbietername:** `compose` (nicht `docker`: der Anbieter ist „Docker-Host über Compose“, `docker` läse man als der Daemon im Rechner).
D2 **Eine Maschine = ein Projekt** (Empfehlung); Gruppen teilen nur das Netz der NATS-Gruppe, nicht die Datei. Alternative: ein Projekt je Gruppe (eine `compose.yaml` mit allen Diensten), was dem Satz „viper up erzeugt das Compose-YAML und fährt Services und Netze hoch“ näher liegt, aber das gleichzeitige Schreiben aus §7.2 mitbringt.
D3 **Zugang:** `viper docker-proxy` mit `docker exec` (Empfehlung), keine veröffentlichten Ports.
D4 **Docker im Rechner:** wie `K8S_DOCKER`: `dind` (Standard, ein privilegierter Dienst), `remote`, `none`; `COMPOSE_DOCKER`.
D5 **Eigene Dienste** (Datenbank, Redis, …): `COMPOSE_EXTRA_FILE` nennt eine zweite Compose-Datei des Nutzers, die `viper` mit `-f` hinter die eigene stellt und im selben Projekt/Netz startet; `viper` prüft nur, dass sie lesbar ist. Nicht im ersten Schritt.
D6 **Einstellungen:** `COMPOSE_IMAGE`, `COMPOSE_DISK`… sinngemäß wie `K8S_*` (zu bestätigen).
D7 **Zustand der NATS-Rolle:** ein Objekt je Zustand mit Name als Sperre (Empfehlung) oder im Server-Container (§5).
D8 **Entfernter Docker-Host:** nicht in Version 1 (§7.7).
D9 **Privilegierter `dind`-Dienst:** der Preflight meldet es als **Fehler**, solange `COMPOSE_DOCKER` nicht ausdrücklich gesetzt ist (auf einem geteilten Docker-Host gibt der Dienst Root auf dem Host).

## 10 Meilensteine

| # | Titel | Inhalt |
|---|---|---|
| 0 | Verifikation | Die Punkte 1 bis 5 der Prüfung sind **schon belegt** (§13). Offen: **1.** `restart: true` bei `depends_on` für den `dind`-Dienst; `wait -n; kill $!` und das Aufräumen verwaister Prozesse im Transport. **2.** Zustand der NATS-Rolle als Objekt (D7). **3.** `fib_trie` mit dem Teilnetz. **4.** Die Entscheidungen aus §9. |
| 1 | Adapter | `compose`-Adapter gegen einen Fake des `docker`-Aufrufs: Datei erzeugen, `up`, `rm`, Leftovers, Preflight |
| 2 | Anbindung | Einstellungen, `sshconfig`, `viper docker-proxy`, Preflight |
| 3 | Echter Lauf | lokales Docker: `up`, `check`, `ssh`, Docker im Rechner, Neustart des Containers (Daten bleiben), `down`; Handbuch |
| 4 | NATS | Gruppennetz, Name im Zertifikat, Zustand der Labels; Lauf mit drei Containern |

## 11 Teststrategie und Aussage über die Qualität

* **Einheitentests** mit einem Fake für den Aufruf von `docker` (Argumente, die erzeugte Datei als YAML gelesen und gegen Referenzdateien verglichen, keine Schlüssel in der Datei oder der Umgebung).
* **Echter Lauf** auf dem lokalen Docker-Host dieser Umgebung (Docker 29.9.3, Compose v5.7.0 *[V]*); das Image-Bauen und `image/test.sh` laufen dort schon.
* **Aussage im Handbuch:** auf dem lokalen Docker-Host getestet, ein entfernter Kontext nutzt dieselben Befehle, ist aber nicht einzeln geprüft; Docker Desktop und rootless Docker nicht geprüft.

## 12 Definition of Done

* Meilenstein 3 und 4 mit echten Läufen, `go test -race ./...`, Linter und `gofmt` grün.
* Handbuch (Aufgabe in Kapitel 3, Nachschlagen in Kapitel 4, Fehlerbehebung in Kapitel 7), READMEs und `architektur.md` nennen den Anbieter, seine Einstellungen, die Grenzen (privilegierter Dienst, kein Docker Desktop geprüft) und die Aussage über die Qualität.
* Nach `down` ist kein Dienst, Netz oder Volume mit `viper-managed=true` übrig.

## 13 Prüfung (Stand 12.11.2026)

Ein unabhängiger Agent hat den Plan gegen den Code geprüft und die offenen Punkte auf dem lokalen Docker-Host (29.9.3, Compose v5.7) ausprobiert. Befunde und was daraus wurde:

1. **Secrets (hoch):** Die kurze Schreibweise legt sie unter `/run/secrets/<name>` ab (das Einstiegsprogramm bricht ab); `uid`/`gid`/`mode` gelten nicht, es sind Bind-Mounts; eine Datei `root:root 0600` ist im Container unlesbar; bei einem entfernten Daemon zeigt der Pfad auf dessen Dateisystem. → §3, §7.7, D8: lange Schreibweise mit `target:`, Dateien für Benutzer 1000 lesbar, **nur ein lokaler Daemon in Version 1**.
2. **`dind`-Dienst (hoch):** funktioniert (Loopback, `healthcheck` in 8 s), bricht aber, wenn der Rechner-Container allein neu startet (der Daemon bleibt im alten Namensraum); kein Volume für `/var/lib/docker`. → §3, §7.8: `restart: true` prüfen, sonst Neustart des Projekts; Images im Daemon gehen verloren (wie bei Kubernetes).
3. **Labels (hoch):** nicht änderbar, Neuanlegen unzumutbar. → §5, D7: Zustand als eigenes Objekt mit Namen als Sperre.
4. **Gemeinsame Netze (mittel):** Compose-eigene Netze mit festem Namen gehören keinem Projekt und bleiben stehen. → §5: `viper` legt und löscht das Netz selbst, Compose bindet es als `external` ein.
5. **Gleichzeitiges `up` (mittel):** zwei Läufe scheitern an Namenskonflikten. → D2 bestätigt (eine Maschine = ein Projekt), Dateisperre je Projekt (§7.9).
6. **Transport (mittel):** trägt eine echte SSH-Sitzung (20 MB, Prüfsumme gleich), hinterlässt aber bei hartem Abbruch Prozesse. → §4.
7. **Eigene Adresse (mittel):** `hostname -i` ist mit zwei Netzen falsch. → §5: Teilnetz als Umgebungsvariable.
8. **Größen (niedrig):** `cpus` und `mem_limit` gelten, nicht zusammen mit `deploy.resources`. → §3.
9. **Volumes (niedrig):** benannte Volumes werden aus dem Image gefüllt (Besitzer 1000), die Markierungen funktionieren. → §3.
10. **Stellen im Code (mittel):** Liste in §6 Punkt 7.
11. **Sicherheit (mittel):** Verzeichnis 0700 für die Dateien mit Schlüsseln, beim `down` entfernen; der privilegierte Dienst ist ein Fehler im Preflight, solange er nicht ausdrücklich gewählt ist (D9); Werte nur über `yaml.v3`. → §2, §6 Punkt 8.
12. **Kleinigkeiten:** `rm -v` entfernt nur anonyme Volumes; Pull-Richtlinie `never` für lokale Images; die ID ändert sich beim Neuanlegen. → §3.

**Richtig an dem Plan:** ein Projekt je Maschine, das Anlegen des Netzes als Sperre (sechs gleichzeitige Aufrufe: eines hat es geschafft), `docker exec -i` mit `bash` als SSH-Transport, `network_mode: service:` für den Daemon, gültige Größenangaben, `external: true` für das Gruppennetz.

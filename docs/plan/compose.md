# Plan: Docker Compose als Anbieter (Rechner als Container auf einem Docker-Host)

**Ziel:** `PROVIDER=compose` richtet einen „Rechner“ als **Container** auf einem Docker-Host ein: dem eigenen Rechner oder einem entfernten (Docker-Kontext). `viper up` **erzeugt dazu die `compose.yaml`** (Dienste, Netze, Volumes, Secrets) und startet sie mit `docker compose up`; `viper down` beendet sie wieder. Wie bei Kubernetes braucht es kein Cloud-Konto, aber auch keinen Cluster: nur Docker.

**Stand:** Entwurf, noch nicht geprüft. Nichts ist umgesetzt. Die Voraussetzungen sind zum Teil schon getestet (§1).

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
| Host-Schlüssel, erlaubte Schlüssel | Compose-`secrets` aus Dateien im Zustandsverzeichnis → `/run/viper/secret/…` *[?: Modus und Besitzer im Container für Benutzer 1000, M0]* |
| Firewall, DNS, Adresse | keine (`DNS_PROVIDER=none` ist Pflicht) |
| Größe (`SERVER_TYPE`) | `<cpu>x<GiB>` → `cpus` und `mem_limit` des Dienstes |
| Image (`SERVER_IMAGE` bei diesem Anbieter) | `COMPOSE_IMAGE` (Pflicht, ohne Standard; `viper image build` baut es **lokal**, kein Import in einen Cluster nötig) |
| Platte | zwei **benannte Volumes** `<name>-home` und `<name>-brew` (Heimverzeichnis und Homebrew, beim ersten Start gefüllt) |
| Docker auf dem Rechner | ein zweiter Dienst `<name>-docker` (`docker:dind`, `privileged`) mit `network_mode: service:<maschine>`, damit `127.0.0.1:2375` im Rechner der Daemon ist; nur auf Loopback gebunden. Alternativ `remote` oder `none` (wie `K8S_DOCKER`) *[(zu bestätigen) D4]* |
| Löschen | `docker compose -p … rm -sf <dienst>` und die Volumes des Dienstes; bei der letzten Maschine des Projekts `down` samt Netzen |

## 4 Der Zugang

Der Container ist über SSH erreichbar wie ein Pod. Zwei Wege:

* **A: `ProxyCommand` mit `docker exec`.** `viper docker-proxy <maschine>` (ein Unterbefehl wie `viper k8s-proxy`, ohne Shell-Quoting im SSH-Konfigurationstext) startet `docker --context … exec -i <container> …` und verbindet Standard-Ein-/Ausgabe mit `127.0.0.1:2222` im Container. Dafür braucht der Container ein Programm, das das kann: `bash` (`/dev/tcp`) oder `nc`. Das Image hat `bash`, `nc` nicht *[V, geprüft]* *[?: Verbindungsende und Halbschließen mit `/dev/tcp`, M0]*. Funktioniert unverändert mit einem entfernten Kontext.
* **B: Port auf `127.0.0.1` des Docker-Hosts.** Einfach, aber bei einem entfernten Host öffnet es einen Port dort, und es kollidiert bei mehreren Maschinen (Portverwaltung).

Empfehlung **A**: wie bei Kubernetes, keine Ports, kein Host-Zustand *[(zu bestätigen) D3]*.

## 5 NATS-Gruppen

* **Netz der Gruppe:** ein Netz `viper-nats-<gruppe>` (`internal: false`, nur die Mitglieder hängen daran), zusätzlich zum Standardnetz des Projekts für den Zugang ins Internet. **Das Anlegen des Netzes ist die Sperre des ersten Starts** *[V, §1]*; Mitglied ist, wer am Netz hängt. Anders als bei Kubernetes gibt es keine Policy, die ein Plugin durchsetzen muss: Ein Container, der nicht am Netz hängt, erreicht den Port nicht.
* **Adresse des Servers:** der Dienstname im Netz (`viper-nats-<gruppe>`, ein Netz-Alias am Server-Dienst); das Zertifikat lautet darauf, der Server lauscht auf der Adresse seines Containers (`$POD_IP`-Gegenstück, siehe unten). Damit gilt dasselbe Modell wie bei Kubernetes: Name im Zertifikat, Adresse darf wechseln.
* **Anpassungen am Code:** Die Variable heißt bei Kubernetes `POD_IP`; ein Container hat keine Downward-API. `entry.sh` setzt sie deshalb aus der eigenen Adresse im Netz der Gruppe (`hostname -i` oder aus `/proc/net/fib_trie`) *[?: M0]*; `natsserver.ListenPodIP` bleibt als Name, oder wird zu einem neutralen Namen umbenannt *[(zu bestätigen)]*.
* **Rolle, Gruppe, Zustand:** Labels des Dienstes/Containers (`viper-nats-group`, `-role`, `-state`); `UpdateNATSMetadata` schreibt sie als Container-Labels *[?: Docker kann Labels eines laufenden Containers nicht ändern; Ausweg: ein kleiner Zustand pro Projekt in einer Datei oder ein Label am Netz/Volume, M0]*. Das ist der unsicherste Punkt dieses Plans.

## 6 Änderungen am vorhandenen Code

1. **Neuer Adapter** `internal/adapter/driven/compose`: ruft das `docker`-Programm auf (über `system.Runner`, wie die SSH-Aufrufe; kein Docker-SDK), erzeugt die Compose-Datei mit `gopkg.in/yaml.v3` (schon in `go.mod`) und implementiert `cloud.Provider` und `cloud.NATSProvider` und `cloud.Leftovers`.
2. **`ProviderInfo`** für `compose` (`Container: true`, NATS) und die Einstellungen `COMPOSE_*` in `settingsfile`; Prüfungen wie bei `K8S_*` (`DNS_PROVIDER=none`, `WORKSPACE_DIR` unter `/home/builder/`, kein `VIPER_SWAP`/`VIPER_CODE_SERVER`).
3. **SSH-Konfiguration:** `sshconfig.WriteConfig` schreibt für diesen Anbieter `viper docker-proxy`; heute fest `k8s-proxy` und Port 2222 *[V]*. Der Anbieter wird an `Machine` erkannt.
4. **`viper docker-proxy`** neben `viper k8s-proxy` (Port `driving.Proxy`, Anbindung in `app`).
5. **Preflight (`Validate`):** `docker` erreichbar, Kontext da, Compose-Plugin da, Image vorhanden (`docker image inspect`), Rechte (Zugriff auf den Docker-Socket), Platz; eine Warnung für `dind` (privilegierter Container, gibt Root auf dem Host).
6. **`viper image build`** kann ohne `--k3d` und ohne Register arbeiten: das Image liegt danach im Docker-Host und wird direkt genutzt *[V: es ist ein lokales `docker build`]*. Bei einem entfernten Kontext wird es dort gebaut (`docker --context … build`) *[?]*.

## 7 Offene Fragen und Risiken

1. **Zustand von Labels (NATS)** wie in §5: eigener Meilenstein 0.
2. **Eine Datei, mehrere Maschinen:** `viper up web` und `viper up api` unabhängig voneinander laufen lassen, während beide in eine Gruppen-Datei schreiben wollen (Gruppe = ein Projekt): gleichzeitiges Schreiben und `docker compose up` desselben Projekts von zwei Prozessen. Bei einer Maschine je Projekt (D2, Empfehlung) fällt es weg, und die Gruppe verbindet nur das **Netz** (`viper-nats-<gruppe>`), nicht ein Projekt.
3. **Privilegierter `dind`-Dienst** gibt Root auf dem Docker-Host (bei Kubernetes: auf dem Knoten). Auf dem eigenen Rechner ist das der Nutzer selbst, auf einem geteilten Host nicht; das Handbuch sagt es.
4. **Docker Desktop (Windows, macOS):** Der Docker-Host ist eine VM; Verhalten von `--privileged`, Volumes und Netzen ist dort nicht einzeln geprüft.
5. **Rootless Docker/Podman als Host:** `--privileged` für `dind` geht nicht; dann `remote` oder `none`.
6. **Verbindungsabbruch des Transports:** `docker exec -i` mit `/dev/tcp` muss halb geschlossene Verbindungen (SSH schließt die Eingabe) sauber beenden.

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

## 10 Meilensteine

| # | Titel | Inhalt |
|---|---|---|
| 0 | Verifikation | **1.** Container aus `viper-machine` mit Secrets als Dateien (Modus, Besitzer für uid 1000) in Compose. **2.** SSH durch `docker exec -i … bash` mit `/dev/tcp`, Verbindungsende. **3.** Zustand der Labels (NATS), §5. **4.** `POD_IP` aus der Adresse im Netz. **5.** `dind`-Dienst mit `network_mode: service:…` und Loopback. **6.** Entscheidungen aus §9. |
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

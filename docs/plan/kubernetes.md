# Plan: Kubernetes als Anbieter (Container statt Maschinen), zuerst mit k3d

**Ziel:** `PROVIDER=kubernetes` startet eine „Maschine“ als **Pod** in einem beliebigen Kubernetes-Cluster (k3d lokal, aber auch GKE, EKS, AKS, Hetzner-Cluster), statt als VM bei einem Cloud-Anbieter. `viper up`, `down`, `check`, `ssh`, `vscode` und die Gruppen sollen dort so arbeiten wie bei den anderen Anbietern. Der Anbieter ist der **Cluster**, nicht die Cloud darunter: `viper` braucht nur eine `kubeconfig`.

**Stand:** Entwurf, Entscheidungen D1 bis D8 getroffen (§9), einmal unabhängig gegen den Code geprüft; die Befunde sind eingearbeitet (§12) und ändern den Plan an mehreren Stellen (die „Naht“ SSH, die Server-ID, NATS-Labels, Docker, Sicherheitskontext, `ProxyCommand`). Meilenstein 0 ist teilweise gelaufen (§13); sonst ist nichts umgesetzt.

**Kennzeichnung:** *[V]* heißt, im Code oder in einem Plan bestätigt. *[?]* heißt Vermutung; jede solche Stelle wird in Meilenstein 0 geprüft, bevor etwas darauf gebaut wird. **(zu bestätigen)** markiert Arbeitsannahmen, die eine Entscheidung brauchen (§9).

Grundlage sind [`nats-anbieter.md`](nats-anbieter.md) (wie ein Anbieter ein Gruppennetz abbildet) und [`gruppen.md`](gruppen.md).

## 1 Ausgangslage

Was `viper up` mit einer Maschine tut *[V, `internal/usecase/start`, `cloud-init.yaml`]*:

1. Schlüssel und Firewall beim Anbieter anlegen (`EnsureLoginKey`, `EnsureFirewall`), den Server anlegen (`CreateServer`) mit **cloud-init als Benutzerdaten**: Benutzer `builder`, erlaubte Schlüssel, Host-Schlüssel samt Zertifikat der Host-CA, Pakete, Docker, `sshd`-Härtung, ein Neustart nach Updates.
2. Die SSH-Konfiguration schreiben (`Host <alias>`, `HostName <IPv4>`, `User builder`, Host-CA oder angeheftet), den DNS-Namen setzen.
3. Auf SSH warten, **auf cloud-init warten**, dann mit `ssh` Skripte ausführen (`internal/usecase/start/scripts`): Git, Homebrew, Claude Code, Auslagerungsdatei (`swap.sh`), Repository klonen, bei NATS `systemd`-Einheit, Zertifikat und MCP-Server.

Alles nach dem Anlegen läuft über **`ssh -F <Konfiguration> <Alias>`** (auch `scp`, `viper ssh`, `viper vscode`, der Tunnel des Betreibers) *[V, `start.go`, `check.go`, `stop.go`, `provision.go`, `vscode.go`, `natsoperator.go`]*. Das ist die Naht dieses Plans. **Sie hat eine Lücke:** die Konfiguration wird aus `Server.IPv4` geschrieben, und eine leere Adresse bricht den Lauf an vielen Stellen ab (§5 Punkt 4). Die Naht gilt erst, wenn diese Stellen angepasst sind.

Was an einer VM hängt und in einem Container nicht gilt:

| Annahme heute | im Container |
|---|---|
| `cloud-init` richtet ein und meldet „fertig“ | gibt es nicht; das Image bringt alles mit, ein Einstiegsprogramm richtet nur das Laufzeit-Material ein |
| öffentliche IPv4-Adresse, DNS-Name, Firewall für Port 22 | keine; Zugang über die API des Clusters |
| `systemd` (NATS-Dienst, `ssh`-Dienst) | kein `systemd` als PID 1; ein Prozessverwalter (`s6`, `tini` mit Skript) |
| `apt`, Updates, Neustart nach Updates | entfällt; Updates sind ein neues Image |
| `ufw` auf dem Server (NATS) | entfällt; Schutz durch NetworkPolicy |
| Docker auf der Maschine **ist Pflicht**: `start` bricht ohne „docker=ok“ ab, `check` meldet es als Fehler *[V, `provision.go`, `probe.sh`]* | nur mit Docker-in-Docker oder privilegiertem Pod; sonst muss die Pflicht für diesen Anbieter entfallen (§5 Punkt 3) |
| `sudo` ohne Passwort für `builder` (NATS, Auslagerungsdatei, Installationen) *[V, `cloud-init.yaml`]* | braucht `allowPrivilegeEscalation`; Pod Security „baseline“ und „restricted“ (GKE, EKS, AKS) verbieten es |
| `code-server` und weitere Dienste unter `systemd` | brauchen den Prozessverwalter oder entfallen |
| Heimverzeichnis aus dem Image (`.tmux.conf`, VS-Code-Einstellungen, `~/.ssh`) | ein Volume darüber verdeckt es; das Einstiegsprogramm legt es bei leerem Volume an |
| Claude Code und Homebrew werden bei der Einrichtung installiert (`runcmd`, Marker `/var/tmp/claude-ready`) | im Image oder von `up`; sonst installiert `verifyStatus` bei fehlendem Marker nach |
| dauerhafte Platte | flüchtig, bis ein Volume eingebunden ist |
| feste private IP (Zertifikat des NATS-Servers hat sie als SAN) | Pod-IP ändert sich bei jedem Neuanlegen |

## 2 Grundregeln

1. **Der Zugang bleibt SSH.** Kein zweiter Weg (`kubectl exec` in jedem Skript). Der Pod führt einen `sshd` aus; `viper` erreicht ihn über einen `ProxyCommand` durch die API des Clusters. Host-CA, Schlüssel, `viper ssh`, `vscode` und die Skripte gelten dann weiter, **sobald die Stellen angepasst sind, die eine IPv4 voraussetzen** (§5 Punkt 4) *[?, VS Code Remote-SSH mit ProxyCommand: M0]*.
2. **Keine Geheimnisse in Befehlszeilen oder Umgebungsvariablen von Pods.** Schlüssel, Host-Schlüssel und Tokens kommen als **Secret**, als Datei eingebunden. (Wie bei den Skripten: nichts im Klartext in der Prozessliste.)
3. **Der Cluster gehört `viper` nicht.** `viper` legt nur an, was es selbst beschriftet (`viper-managed=true`), und löscht nur das. Kein Cluster, kein Namespace außer dem eingestellten, keine Knoten, keine Zusatzdienste.
4. **Ehrlich zur Qualität.** Bei k3d ist ein echter Lauf möglich und wird zur Abnahme verlangt. Für GKE/EKS/AKS gilt nur: gleiche API, nicht einzeln geprüft (§7).

## 3 Abbildung auf Kubernetes

| `viper` | Kubernetes |
|---|---|
| Server | **StatefulSet** mit einem Replikat (stabiler Name, ein Volume), Name `<SERVER_PREFIX>-<maschine>` *[(zu bestätigen) D2]* |
| `Server.ID` | die **UID** des StatefulSet, nicht der Name: `start` erkennt mit der ID, ob ein Server schon eingerichtet ist (Markierung `ready`), und `ForgetStalePin` verwirft damit alte Anheftungen *[V, `steps.go`, `sshconfig`]*; ein gleichbleibender Name ließe einen neuen Pod als „fertig“ erscheinen |
| Login-Schlüssel (`EnsureLoginKey`) | der Schlüssel selbst als Referenz (wie Azure: kein Schlüsselobjekt); er geht mit dem Pod in ein Secret |
| Host-Schlüssel und Zertifikat | im selben Secret, vom Einstiegsprogramm an die Stelle gelegt, die `sshd` liest |
| Firewall (`EnsureFirewall`) | keine eingehende Verbindung von außen, also nichts zu öffnen: die Methode meldet „vollständig“ und tut nichts. `SSH_ALLOWED_CIDRS` gilt hier nicht (eine Meldung sagt es) |
| öffentliche Adresse, DNS | keine. `DNS_PROVIDER=none` ist für diesen Anbieter Pflicht; `Server.IPv4` bleibt leer |
| Zugang | neues Feld `Server.Proxy`: der `ProxyCommand` der SSH-Konfiguration, z. B. `viper k8s-proxy <kontext> <namespace> <pod>` (§4) |
| Platte | ein PersistentVolumeClaim, eingebunden unter `/home/builder` und `WORKSPACE_DIR`. **Ein PVC aus `volumeClaimTemplates` wird mit dem StatefulSet nicht gelöscht:** `viper` legt ihn selbst an (Label `viper-managed`) und löscht ihn bei `down` ausdrücklich, auch nach einem halb gescheiterten `down` beim nächsten `up`/`down`; die Abfrage „alle Daten werden zerstört“ gilt unverändert *[(zu bestätigen) D3]* |
| Größe (`SERVER_TYPE`) | `<cpu>x<ram>`, zum Beispiel `2x8` (2 CPU, 8 GiB) als Requests und Limits; frei wählbar, es gibt keine Preisliste |
| Standort (`SERVER_LOCATION`) | der Kontext der `kubeconfig` (`K8S_CONTEXT`); der Namespace ist `K8S_NAMESPACE` |
| Image (`SERVER_IMAGE`) | das Container-Image (`K8S_IMAGE`), nicht „ubuntu-24.04“ |

Das Image (Dockerfile im Repository, `make image`): Ubuntu, die Pakete aus `cloud-init.yaml`, Benutzer `builder`, `sshd` mit derselben Härtung (die Datei `01-keys-only.conf` kommt aus **einer** Quelle für VM und Image; ein Test prüft die wirksame Konfiguration mit `sshd -T`), `tini`/`s6` als PID 1, Sicherheitskontext: **der Pod läuft als `builder` (uid 1000), ohne Rechte** (`runAsNonRoot`, `allowPrivilegeEscalation: false`, alle Capabilities entfernt, Seccomp `RuntimeDefault`). Der `sshd` läuft dann ebenfalls als `builder` auf Port 2222 (§14). Ein Pod mit root, `sshd` auf Port 22 und `sudo` wäre die zweite Möglichkeit; sie ist in „restricted“-Clustern verboten und wird nicht gebaut, ein Einstiegsprogramm, das das Secret einliest, die Host-Schlüssel mit Zertifikat ablegt und `sshd` startet, und `/run/viper-ready`, sobald es fertig ist. nats-server und `nats`-Werkzeug sind schon im Image (statt `natsinstall.sh`).

## 4 Der Zugang

`ProxyCommand` verbindet `ssh` mit Port 22 des Pods durch die API: `kubectl port-forward` oder `kubectl exec -i … -- nc localhost 22`. Zwei Wege:

* **A: `kubectl` im `ProxyCommand`.** Einfach; verlangt `kubectl` auf dem Rechner des Nutzers.
* **B: ein Unterbefehl von `viper` (`viper k8s-proxy`)**, der mit `client-go` den Port-Forward macht und Standard-Ein-/Ausgabe verbindet. Braucht kein `kubectl`, gleiche Zugangsdaten wie der Anbieter.

Empfehlung **B** *[(zu bestätigen) D4]*: derselbe Code wie der Anbieter, nur eine Abhängigkeit, ein Test mit der Fake-API möglich. Folge: `viper` in der SSH-Konfiguration muss als absoluter Pfad stehen.

`Server.Proxy` ist neu *[V: `model.Server` hat es nicht]*; `sshconfig.WriteConfig` schreibt dann `ProxyCommand` und als `HostName` den Podnamen (nur als Name, er wird nicht aufgelöst). Die Host-Schlüsselprüfung bleibt (`HostKeyAlias`, Host-CA). Das Feld reicht nicht allein:

* `ServerByName` liefert den Proxy auch für einen vorhandenen Server; `natsgroup.Peer` braucht den Proxy **jeder** Gegenstelle.
* **Einschleusen in die SSH-Konfiguration:** `sshconfig.quote` setzt nur bei Leerzeichen Anführungszeichen und schützt weder `"` noch `%` (ssh ersetzt `%h` u. ä.) noch Zeilenumbrüche *[V, `sshconfig.go`]*. Kontext und Namespace sind frei wählbar, Maschinennamen nicht. Darum: Kontext, Namespace und Podnamen gegen DNS-1123 bzw. die Regeln der `kubeconfig` prüfen, `%` als `%%` schreiben, und der `ProxyCommand` nennt **nur Maschinenname und Gruppendatei** (`viper k8s-proxy <maschine>`), den Kontext liest `viper` selbst aus den Einstellungen.
* Der absolute Pfad von `viper` in `~/.ssh/config` veraltet, wenn die Datei verschoben wird; geschrieben wird er bei `up`, `check` und `down`, nicht bei jedem Aufruf. Der Proxy startet `viper` über `PATH`, wenn der Pfad nicht mehr stimmt, und sagt es.

## 5 Änderungen am vorhandenen Code

1. **Neuer Adapter** `internal/adapter/driven/kubernetes` mit `client-go`; `app/provider.go` und `model/provider.go` kennen `kubernetes` *[V: so sind die anderen angebunden]*.
2. **`ServerSpec`** bekommt strukturiert, was cloud-init heute als Text trägt (erlaubte Schlüssel, Host-Schlüssel, Zertifikat), neben `UserData`. Es geht auch ohne: der Adapter könnte den fertigen Text zerlegen *[V: `userdata.go` setzt ihn aus genau diesen Teilen zusammen]*; die drei Felder sind sauberer und kleiner als ein Parser für YAML. **(zu bestätigen)**
3. **„Container“-Fähigkeit** (`ProviderInfo.Container`): sie muss mehr abdecken, als zuerst geplant — das Warten auf cloud-init und den Neustart (`reboot-required`), die Pflicht „Docker läuft“ (`provision.go`, `probe.sh`, Meldung in `check`), die Meldung „cloud-init: no status“ in `check`, `swap.sh`, `systemctl`-Aufrufe für `code-server` und NATS. Statt der Wartezeit auf cloud-init wartet `start` auf `/run/viper-ready`. Für Docker gibt es heute **keine** Einstellung *[V]*; sie entsteht hier (`VIPER_DOCKER`, Standard „aus“ nur bei diesem Anbieter).
4. **Alles, was `Server.IPv4` voraussetzt** (jede dieser Stellen bricht heute mit leerer Adresse ab oder zeigt eine leere Adresse) *[V]*: `steps.go` (`ipOrFail`, „has no IPv4 address“), `start.go` (`announceServer`, `pinHostKey`: `WriteConfig(c, ip)`), `check.go`, `stop.go` (Ausgaben und `WriteConfig`), `natsgroup.go` (`Peer`: „has no public address“, `WriteConfig`), `cli/native.go` („address is ''“), `nodns.Set` („server is at ''“). Der Port `WriteConfig(m, ip string)` hat sechs Aufrufer und eine zweite Signatur in `servertest`; er bekommt die ganze `Server`-Angabe (Adresse **oder** Proxy). Das ist der größte Teil der Arbeit in Meilenstein 3.
5. **`Server.Proxy`** und `sshconfig` (§4); `check` testet den öffentlichen NATS-Port nicht, wenn es keine öffentliche Adresse gibt *[V: `natsPortCheck` überspringt eine leere Adresse schon]*.
6. **`settingsfile`**: `K8S_CONTEXT`, `K8S_NAMESPACE` (Standard `viper`), `K8S_STORAGE_CLASS`, `K8S_IMAGE`, `K8S_DISK_GB`; `DNS_PROVIDER` muss `none` sein; `SSH_ALLOWED_CIDRS` wird ignoriert. `DNSHost` ist bei `DNS_PROVIDER=none` und leerer `DNS_DOMAIN` `name.` und dient trotzdem als `HostKeyAlias` und Zertifikats-Principal *[V, `config.go`, `userdata.go`]*: es funktioniert, ist aber ein Sonderfall, den ein Test festhält.
7. **Preflight** (`Validate`): `kubeconfig` lesbar, Kontext und Namespace da, Rechte mit `SelfSubjectAccessReview` (StatefulSet/Secret/PVC anlegen und löschen, `pods/portforward`) — das ist eine echte lesende Rechteprüfung, anders als bei den Cloud-Anbietern —, StorageClass da, Image genannt, Pod-Security-Stufe des Namespace (Label `pod-security.kubernetes.io/enforce`) als Warnung, wenn sie `restricted` ist.
8. **`viper find`** kennt den Anbieter nicht (keine Preise); die Anbietertabelle sagt es.

## 6 NATS-Gruppen im Cluster

Eigener Abschnitt, weil er am meisten ändert, und **eigener Meilenstein am Schluss** (M5). Ohne ihn läuft `PROVIDER=kubernetes` mit `NATS_ENABLED=false`; `settingsfile` verweigert NATS dann klar, bis M5 fertig ist.

* **Sperre des ersten Starts:** Kubernetes legt Objekte **atomar** an (`409 AlreadyExists`) *[?, M0 prüfen für NetworkPolicy]*. Das Gruppennetz ist eine NetworkPolicy `viper-nats-<gruppe>`; ihr Anlegen ist die Sperre. Das ist die sauberste Sperre aller Anbieter.
* **Gruppenmitgliedschaft:** Label `viper-nats-group` an der **Pod-Vorlage und am StatefulSet**, nicht nur am Pod: ein Label, das nur am Pod hängt, ist weg, sobald der Cluster den Pod neu erzeugt. Die NetworkPolicy erlaubt den NATS-Port nur zwischen Pods mit diesem Label. Rolle und Zustand stehen als Labels am StatefulSet (`UpdateNATSMetadata` schreibt dorthin, `ListGroupMachines` liest von dort; ein Merge-Patch ist je Schlüssel atomar). Eine Änderung der Pod-Vorlage startet den Pod neu; darum gehören Rolle und Zustand an die Metadaten des StatefulSet, nur die Gruppe an die Vorlage. `InNetwork()` und `AttachPrivateNetwork` (`start/nats.go`) bekommen eine Bedeutung für Pods (Label gesetzt und Pod bereit).
* **Der Server läuft ohne `systemd`:** unter dem Prozessverwalter des Images. `natsunit.sh` und `check` (`systemctl is-active`) brauchen eine Variante.
* **Adresse und Zertifikat:** Die Pod-IP ändert sich; das Zertifikat hat heute die private IP als SAN *[V, `natscert.sh`]*. Im Cluster ist die Adresse der **Dienstname** (ein headless Service `viper-nats-<gruppe>` auf den Server-Pod, mit einer Bereitschaftsprüfung, denn ein headless Service löst nur auf bereite Pods auf); das Zertifikat bekommt den DNS-Namen als SAN. Der Server **lauscht** weiter auf der Pod-IP (aus der Downward-API), das **angekündigte** Ziel ist der Dienstname: heute ist beides derselbe Wert *[V]*, und das muss getrennt werden. Betroffen sind: `natsserver.go` (nur eine private IPv4 als Adresse zugelassen, `Render` schreibt dieselbe Adresse als `host:`), `ParseEndpoint` (liefert diese Adresse als Tunnelziel und `TLSServerName`), `natscert.sh` (`IP:$ip`, Prüfung mit `grep "IP Address:"`), `natsstatus.sh` und `natschecks.go` (`cert_ip`, `natsListenCheck`), `natsunit.sh` (`ss … $ip:$port`), `natsconfig.sh` und `natsstatus.sh` (`systemctl`), `natsinstall.sh` (entfällt, das Image bringt `nats-server` mit). **Das größte Risiko des Plans.**
* **Betreiber:** Der SSH-Tunnel (`ssh -N -L`) funktioniert durch den `ProxyCommand` unverändert *[?, M0]*.

## 7 Teststrategie und Aussage über die Qualität

* **Einheitentests** gegen die Fake-Clientmenge von `client-go` (`k8s.io/client-go/kubernetes/fake`): Anlegen, Finden, Löschen, Secret-Inhalt (keine Schlüssel in Umgebungsvariablen oder Befehlszeilen), Beschriftung, Löschen nur eigener Objekte, Fehlerabbildung.
* **Image-Test ohne Cluster:** `docker run` des Images, `ssh` mit Host-Zertifikat und Schlüssel; das prüft Einstiegsprogramm und `sshd`-Härtung, bevor Kubernetes dazukommt.
* **Echter Lauf auf k3d** (Abnahme): `viper up`, `check`, `ssh`, Neustart des Pods (Daten bleiben), `down` (Volume weg). Hier sind Docker und `kubectl` (über Homebrew) vorhanden, `k3d` fehlt *[V, geprüft]*; es käme ebenfalls über Homebrew (`brew install k3d`) oder mit Prüfsumme in ein eigenes Verzeichnis. Für den Cluster gibt es ein fertiges Image (`rancher/k3s`, `k3d` zieht es selbst); eigenes Image braucht nur der Pod der Maschine (§3). Der Entwicklungscontainer läuft mit `--privileged` und einem eigenen Docker-Daemon (`.devcontainer/devcontainer.json`), ist also **kein** „Docker von außen“; der API-Server von k3d sollte auf `localhost` erreichbar sein *[?, M0 bestätigt es]*. **Grenze:** der Container ist auf 4 GB RAM und 2 CPU begrenzt *[V, `devcontainer.json`]*; k3d plus ein Pod mit `2x8` passen nicht hinein, darum ist die Größe in k3d `1x2`.
* **Aussage im Handbuch:** „in k3d getestet; andere Cluster nutzen dieselbe API, sind aber nicht einzeln geprüft“. Grenzen von k3d: kein Netz zwischen Knoten, keine Cloud-Volumes; k3s setzt NetworkPolicy standardmäßig durch (eingebauter Controller) *[?, M0]*. Die Pod-Security-Stufe von k3d ist offen (keine Erzwingung); ob der Anbieter in „restricted“-Clustern läuft, ist damit **nicht** belegt (§8).

## 8 Bewusst nicht enthalten

* Cluster anlegen oder verwalten (`k3d cluster create` bleibt Sache des Nutzers); Helm, Operatoren, Ingress, Zusatzdienste.
* Mehrere Architekturen und ein Image-Register; das Image kommt aus `make image`, bei k3d per `k3d image import`, sonst aus dem eingestellten Register.
* GPU, Knotenwahl, Tolerations (nur `K8S_NODE_SELECTOR` später, auf Anfrage).
* Docker ohne privilegierten Container: geht nach den Tests nicht (§15). `sudo` im Pod: gibt es nicht; was die VM mit `sudo` einrichtet, steckt fertig im Image (§14, §15).
* Preise in `viper find`.

## 9 Entscheidungen

**Entschieden am 12.11.2026** (Vorschläge des Anwenders bestätigt; die Ergebnisse von Meilenstein 0 und der Risikountersuchung, §13 bis §16, sprechen dafür):

D1 **Zugang:** `sshd` im Pod und `ProxyCommand` durch die API statt `kubectl exec` in jedem Skript. `sshd` läuft als `builder` auf **Port 2222** (§14). VS Code Remote-SSH über den Proxy wird am Anfang von Meilenstein 3 geprüft.
D2 **StatefulSet** mit einem Replikat statt Pod, **mit einem von `viper` selbst angelegten PVC** (als vorhandenes Volume eingebunden, nicht über `volumeClaimTemplates`): so hat das Volume eine klare Lebensdauer.
D3 **Volume:** ein PVC für `/home/builder`, am Arbeitsverzeichnis (`WORKSPACE_DIR`) per `subPath` eingebunden; Größe `K8S_DISK_GB` (Standard 20); mit `down` ausdrücklich gelöscht, nach der Abfrage „DELETE“; ein Rest eines halb gescheiterten `down` wird beim nächsten `up` oder `down` aufgeräumt. Das Einstiegsprogramm setzt Besitzer und Rechte des Heimverzeichnisses und legt bei leerem Volume die Dateien aus dem Image an (§17).
D4 **`client-go` für den Adapter, `viper k8s-proxy <maschine>` für den Zugang** (Port-Forward, Standard-Ein-/Ausgabe, kein `kubectl` nötig). Rückfall, falls der Port-Forward über Stdin/Stdout Probleme macht: `kubectl exec -i … -- nc localhost 2222` (in Meilenstein 0 belegt).
D5 **Docker:** `K8S_DOCKER=dind|remote|none` (§15). **Standard in k3d und eigenen Clustern: `dind`**, ein privilegierter Docker-Daemon als zweiter Container im Pod; der Maschinen-Container bleibt ohne root. Das Handbuch nennt die Grenze (privilegierter Container, Namespace mit Pod Security `privileged`). `VIPER_DOCKER` („Docker ist Pflicht“) folgt aus `K8S_DOCKER`.
D6 **NATS:** gehört zur **Abnahme** (die Agenten koordinieren sich darüber). Die Programme sind schon im Image (§17). Der Rest (Dienst ohne `systemd`, Zertifikat mit Dienstnamen, NetworkPolicy als Gruppennetz) ist Meilenstein 5 und kommt nach dem echten Lauf ohne NATS (M4), weil er auf Adapter und Anbindung aufbaut. Bis dahin verweigert `settingsfile` NATS bei diesem Anbieter mit klarer Meldung.
D7 **Image:** `make image` baut `viper-machine:<version>`; bei k3d `k3d image import`; bei anderen Clustern `K8S_IMAGE` aus einem Register der Wahl. Ein veröffentlichtes Image gibt es zunächst nicht. Basis mit Digest festgelegt. Zuerst `amd64`; `arm64` kommt mit Meilenstein 6 (§16).
D8 **Einstellungen:** `PROVIDER=kubernetes`; **`SERVER_LOCATION` ist der Kontext der `kubeconfig`, ohne Standard** (Schutz vor einem Lauf im falschen Cluster); `SERVER_TYPE` ist `<cpu>x<ram>` (Standard `2x4`); `K8S_NAMESPACE` (Standard `viper`, wird beschriftet angelegt, wenn er fehlt, und von `viper` nie gelöscht); `K8S_IMAGE` (Pflicht); `K8S_STORAGE_CLASS` (Standard des Clusters); `K8S_DISK_GB` (Standard 20); `K8S_DOCKER`. Es gibt **kein** `K8S_CONTEXT`.

## 10 Meilensteine

| # | Titel | Inhalt |
|---|---|---|
| 0 | Verifikation | **1.** `k3d` installieren (`kubectl` ist da), Cluster mit dem fertigen k3s-Image anlegen, in dieser Umgebung erreichbar? **2.** `sshd` ohne `systemd` im Container mit Host-Zertifikat und `AuthenticationMethods publickey`. **3.** `ProxyCommand` mit Port-Forward: `ssh`, `scp`, VS Code Remote-SSH. **4.** NetworkPolicy-Anlegen als atomare Sperre (zwei gleichzeitige Aufrufe); Durchsetzung der Policy in k3d. **5.** Daten bleiben nach Neustart des Pods; ein leeres Volume wird mit den Dateien des Heimverzeichnisses angelegt; `down` hinterlässt keinen PVC. **6.** `sudo` und `sshd` mit den nötigen Rechten, `code-server`/`systemctl`-Ersatz, Claude Code und Homebrew im Image. **7.** Speicher: k3d plus Pod in 4 GB RAM. **8.** Entscheidungen aus §9. |
| 1 | Image | **erledigt (§17):** Dockerfile, Einstiegsprogramm, `make image`, `make image-test` |
| 2 | Adapter | **erledigt (§18):** `kubernetes`-Adapter gegen die Fake-API: Anbieter ohne NATS |
| 3 | Anbindung (**erledigt, §19**) | `ServerSpec`, `Server.Proxy`, `WriteConfig` mit ganzer `Server`-Angabe und alle Stellen aus §5 Punkt 4, „Container“-Fähigkeit in `start`/`check` (§5 Punkt 3), `K8S_*` in `settingsfile`, `viper k8s-proxy`, Quoting der SSH-Konfiguration |
| 4 | Echter Lauf | **erledigt (§19)** k3d: `up`, `check`, `ssh`, Neustart, `down`; das Handbuch folgt (offen) |
| 5 | NATS | **erledigt (§20):** Dienstname und Zertifikat, Prozessverwalter, NetworkPolicy als Gruppennetz; Lauf mit drei Pods |
| 6 | Verwaltete Cluster (**nicht ohne Konto möglich; die Einstellungen und der Preflight stehen, §20**) | EKS, GKE, AKS nach §16: Einstellungen für Knotengruppe und Image-Zugang, Preflight, Handbuch; **Lauf nur, wenn ein Konto da ist** |

## 11 Definition of Done

* Meilenstein 4 mit echtem Lauf in k3d, `go test -race ./...` und der Linter grün, `gofmt` geprüft.
* **Meilenstein 5 mit echtem Lauf in k3d:** eine NATS-Gruppe aus drei Pods, `viper nats send`/`broadcast`/`request` und `viper check`.
* Handbuch und `architektur.md` nennen den Anbieter, seine Einstellungen, die Grenzen (kein DNS, keine IPv4, kein Docker ohne Zusatz) und die Aussage über die Qualität.
* Ein Cluster, den `viper` nicht angelegt hat, bleibt unverändert: nach `down` ist kein Objekt mit `viper-managed=true` übrig.

## 12 Prüfung (Stand 12.11.2026)

Ein unabhängiger Agent hat den Plan gegen den Code geprüft. Befunde und was daraus wurde:

1. **Naht falsch für IPv4 (hoch):** Die Konfiguration entsteht aus `Server.IPv4`, und eine leere Adresse bricht an vielen Stellen ab. → §1, §2 Regel 1, §5 Punkt 4 (alle Stellen), Meilenstein 3.
2. **Docker ist Pflicht in `start` und `check` (hoch):** und es gibt keine Einstellung dafür. → §1 Tabelle, §5 Punkt 3, D5.
3. **`Server.ID` als Name falsch (hoch):** die ID steuert die Markierung „eingerichtet“ und das Verwerfen alter Anheftungen; ein PVC aus `volumeClaimTemplates` überlebt das StatefulSet. → §3 (UID, PVC selbst anlegen und löschen), D3.
4. **NATS-Labels am Pod gehen beim Neuerzeugen verloren (hoch):** → §6 (Labels am StatefulSet und an der Vorlage).
5. **Image und PVC (mittel-hoch):** das Volume verdeckt das Heimverzeichnis; `sudo`/`sshd` brauchen Rechte, die Pod Security „restricted“ verbietet; Claude und Homebrew werden heute bei der Einrichtung installiert. → §1 Tabelle, §3 Image, §8, M0 Punkt 5 und 6.
6. **`Server.Proxy` mehr als ein Feld (mittel):** vorhandene Server, Gegenstellen anderer Namespaces. → §4.
7. **Einschleusen über `ProxyCommand` (mittel):** `sshconfig.quote` schützt nur Leerzeichen. → §4.
8. **NATS unvollständig beschrieben (mittel):** Hör- und Ankündigungsadresse sind heute ein Wert; viele Skripte und Prüfungen. → §6.
9. **Sicherheit (niedrig-mittel):** das Secret mit dem Host-Schlüssel liegt in etcd (gleich zu den Benutzerdaten einer VM, hier als Hinweis); die Härtung der `sshd`-Konfiguration aus einer Quelle. → §3 Image.
10. **`DNSHost` bei `DNS_PROVIDER=none` (niedrig):** → §5 Punkt 6.

Zur Testumgebung: Der Entwicklungscontainer hat einen **eigenen** Docker-Daemon (`--privileged`), k3d sollte also laufen; die Grenze ist der Speicher (4 GB). → §7.

## 13 Ergebnisse Meilenstein 0 (Stand 12.11.2026, teilweise)

Gelaufen in dieser Umgebung mit `k3d` v5.9.0 (k3s v1.35.5), ein Server-Knoten, ohne Traefik und Metrics-Server; der Cluster ist wieder gelöscht. Ein Wegwerf-Pod aus `ubuntu:24.04` mit `sshd` als Hauptprozess.

| Punkt | Ergebnis |
|---|---|
| 1 Cluster hier erreichbar | **ja**, in 13 Sekunden angelegt, `kubectl` erreicht ihn auf `localhost` |
| 2 `sshd` ohne `systemd`, Host-Zertifikat, nur Schlüssel | **ja**: Host-Schlüssel und Zertifikat (von der Host-CA signiert) aus einem Secret, `HostCertificate` gelesen, die Prüfung mit `@cert-authority` ging durch, ein falscher Name wurde abgelehnt |
| 3 `ProxyCommand` durch die API | **ja** für `ssh` und `scp` mit `kubectl exec -i … -- nc localhost 22`. **Nicht geprüft:** VS Code Remote-SSH, der Weg über `kubectl port-forward` (der Plan sieht ihn für `viper k8s-proxy` vor), `ssh -L` |
| 4 NetworkPolicy als Sperre | **ja**: sechs gleichzeitige `create` ergaben ein „created“ und fünf „AlreadyExists“. **Nicht geprüft:** ob k3d die Policy durchsetzt |
| 5 Daten bleiben | **ja**: eine Datei im Volume überlebte das Löschen des Pods. Ein PVC aus `volumeClaimTemplates` **bleibt nach dem Löschen des StatefulSet stehen** (bestätigt) und muss ausdrücklich gelöscht werden |
| 6 `sudo`, `code-server`, Claude, Homebrew | offen |
| 7 Speicher | offen (die Maschine hatte hier 31 GB; die Grenze des Entwicklungscontainers ist 4 GB) |

**Neuer Befund:** Das Volume von k3d (`local-path`) legt das Verzeichnis mit Rechten `777` und Besitzer `root` an. `sshd` lehnt dann den Schlüssel ab („bad ownership or modes for directory /home/builder“). Das Einstiegsprogramm muss Besitzer und Rechte des Heimverzeichnisses selbst setzen (`chown builder`, `chmod 755`), nicht nur Dateien bei leerem Volume anlegen. Dieser Punkt gehört in Meilenstein 1.

Außerdem: Ein Pod gilt als bereit, sobald der Container läuft, nicht wenn `sshd` läuft. `viper` wartet auf `/run/viper-ready` (§5 Punkt 3); eine Bereitschaftsprüfung (Port 22) wäre die saubere Ergänzung.

## 14 Ergebnisse der Risikountersuchung (Stand 12.11.2026)

Gelaufen mit `k3d` v5.9.0 (k3s v1.35.5) in dieser Umgebung; Cluster und Test-Images sind wieder gelöscht. Die Namespaces hatten Pod Security `restricted`, das k3s wirklich erzwingt (ein Pod mit root und ohne Sicherheitskontext wurde mit allen vier Verstößen abgelehnt).

| Risiko | Ergebnis |
|---|---|
| **Pod Security „restricted“** (`sshd` als root, `sudo`) | **Lösbar, mit einer Änderung am Entwurf:** ein Pod mit `runAsNonRoot`, `allowPrivilegeEscalation: false`, ohne Capabilities und mit Seccomp `RuntimeDefault` läuft. Darin läuft `sshd` als `builder` auf **Port 2222** (kein Port unter 1024, kein `chroot`/`setuid`), mit `UsePAM no`, eigenen Host-Schlüsseln samt Host-Zertifikat und `AuthorizedKeysFile` aus dem Heimverzeichnis. `ssh`, `scp` und `ssh -L` gingen durch den Proxy. **Zwei Folgen:** (1) `StrictModes no` ist nötig, weil ein Volume mit `fsGroup` das Verzeichnis gruppen- oder weltbeschreibbar anlegt und ein Benutzer ohne Rechte es nicht ändern kann; im Pod mit nur einem Benutzer verliert die Prüfung ihren Zweck; (2) **`sudo` gibt es nicht** („no new privileges“): was die Skripte heute mit `sudo` tun (`swap.sh`, `natsunit.sh`, `natsinstall.sh`, Installationen) muss entfallen oder im Image fertig sein. Auch SFTP muss in der `sshd`-Konfiguration stehen (sonst scheitert `scp`). |
| **NetworkPolicy wird in k3d durchgesetzt?** | **Ja:** mit der Policy kam ein Pod der Gruppe an den Port, ein Pod einer anderen Gruppe nicht (ohne Policy beide). Das Gruppennetz als NetworkPolicy schützt in k3d also wirklich. In anderen Clustern hängt es vom Netz-Plugin ab. |
| **NATS-Zertifikat mit Dienstnamen** | **Ja:** Zertifikat mit `DNS:`-SAN, Server lauscht nur auf der Pod-IP (`host: $POD_IP`, ohne Anführungszeichen in der NATS-Konfiguration), ein headless Service löst **nur bereite Pods** auf (ohne Bereitschaftsprüfung gab es keine DNS-Antwort), `nats pub` über den Dienstnamen funktionierte. Über die Pod-IP scheitert die Prüfung („doesn't contain any IP SANs“), wie gewünscht. Nach dem Neuanlegen des Pods (neue IP) ging dasselbe Zertifikat weiter. Damit ist §6 machbar; die Arbeit liegt in der Trennung von Hör- und Ankündigungsadresse und in den Skripten. |
| **Zugang über `client-go`/Port-Forward** | **Ja:** ein kleines Programm mit `portforward` verband Standard-Ein-/Ausgabe mit Port 2222 des Pods; `ssh` und `scp` gingen durch. Ein Aufruf von `kubectl` ist nicht nötig. Größe: das Test-Programm allein ist 38 MB, `viper` ist heute 84 MiB; der Zuwachs steckt in den gemeinsamen Teilen und ist noch zu messen. |
| **Speicher** | Der k3d-Server mit ein paar Pods belegt rund 600 MiB; der Entwicklungscontainer ist auf 4 GiB begrenzt. Eine Maschine mit `1x2` passt; `2x8` nicht. |
| **Stellen, die eine IPv4 voraussetzen** | gezählt (ohne Tests, ohne Anbieteradapter und `find`): `start/start.go` (4 Stellen: 158, 215, 218, 245), `start/steps.go` (143), `stop/stop.go` (5: 88, 90, 160, 186, 198), `check/check.go` (5: 40, 41, 125, 127, 186), `check/natschecks.go` (350), `natsgroup/natsgroup.go` (80, 83), `port/driven/sshconfig` (15), `sshconfig` (24), `app/app.go` (154), `natsserver.go` (200, 209: nur private IPv4 erlaubt). Das sind etwa 20 Stellen in 8 Paketen; die Arbeit ist überschaubar, aber quer durch `start`, `stop`, `check` und `natsgroup`. |

**Noch offen:** VS Code Remote-SSH über den Proxy (ohne grafische Oberfläche nicht prüfbar), `code-server`, Claude Code und Homebrew im Image, Docker im Pod, wie lange das NATS-Zertifikat im Cluster gilt (31 Tage heute) und wie es erneuert wird.

**Änderung am Entwurf:** Das Image und der Pod laufen **ohne root und ohne `sudo`**; `sshd` hört auf Port 2222. Der `ProxyCommand` und die SSH-Konfiguration nennen diesen Port. Das ersetzt die Annahme in §3, der Pod brauche Rechte für `sshd`, und nimmt die Grenze „Pod Security restricted“ aus §8.

## 15 Root, Homebrew und Docker (Stand 12.11.2026)

Frage: Kommen wir in den Containern ohne root aus, geht Homebrew, und Docker **muss** gehen. Getestet in k3d mit Pod Security `restricted` bzw. `privileged`; Cluster und Images sind wieder gelöscht.

**Ohne root:**

| Wer braucht heute root oder `sudo` *[V, `grep sudo` in Skripten und `provision.go`]* | im Pod |
|---|---|
| `natsinstall.sh`, `natscli.sh` (`sudo install` nach `/usr/local/bin`) | `nats-server` und `nats` sind im Image |
| `natsunit.sh`, `natsconfig.sh` (`systemd`-Einheit, `systemctl reload`) | `nats-server` läuft als Prozess unter dem Prozessverwalter; Neuladen mit Signal (`SIGHUP`) statt `systemctl` |
| `swap.sh` | entfällt |
| `provision.go` `sudo install -d …` (Arbeitsverzeichnis), `code-server` (`.deb`, `systemctl`) | das Arbeitsverzeichnis liegt unter `/home/builder` (auf dem Volume) und braucht kein root; `code-server` entfällt oder kommt als Benutzerinstallation ins Image (VS Code Remote-SSH braucht ihn nicht) |
| `check/probe.sh` (`sudo sshd -T`) | für Container eine eigene Prüfung (Konfigurationsdatei lesen) |
| Claude Code (`install.sh`) | **geht ohne root** (läuft schon als `builder`) |

**Ergebnis: Ja, zur Laufzeit ist kein root nötig.** Alles, was root braucht, gehört in den Bau des Images.

**Homebrew:** **geht.** Das Image legt `/home/linuxbrew/.linuxbrew` als root an, übergibt es `builder` und installiert Homebrew als `builder` mit dem offiziellen Installer (1 Min 19 s beim Bauen). Im Pod mit Pod Security `restricted` (nicht root, keine Capabilities, kein `sudo`) installierte `brew install jq hello` Bottles zur Laufzeit; beides lief. Voraussetzung im Image: `build-essential`, `curl`, `git`, `file`, `procps`. Das Verzeichnis von Homebrew liegt im Image, nicht im Volume (sonst ginge es mit dem Volume verloren, wenn es neu gebunden wird; eine Entscheidung für Meilenstein 1).

**Docker:**

| Weg | Ergebnis |
|---|---|
| Rootless Docker ohne Rechte, `restricted` | **nein:** `rootlesskit` scheitert am Seccomp-Profil (`fork/exec /proc/self/exe`), mit `Unconfined` an `newuidmap` (braucht `SETUID`/`SETGID` und Privilegieneskalation) |
| Rootless Docker mit `SETUID`, `SETGID`, `allowPrivilegeEscalation`, Seccomp und AppArmor `Unconfined`, Host-Netz (`--net=host`, `--bridge=none`, ohne `slirp4netns`, weil das `/dev/net/tun` braucht) | Daemon **startet** als uid 1000, aber **Container scheitern**: `error mounting "proc" … operation not permitted` (ein Pod maskiert `/proc`; nötig wären `procMount: Unmasked` und `hostUsers: false`, und die Bereichsgrenzen der Benutzer-IDs passen dann nicht zu `/etc/subuid`). Nicht weiter verfolgt. |
| Rootful Docker im Benutzer-Namensraum (`hostUsers: false`, root im Namensraum, viele Capabilities) | **nein:** `/sys/fs/cgroup` ist schreibgeschützt (`mkdir … Read-only file system`) |
| **Privilegierter `docker:dind`-Sidecar** | **ja.** `docker run hello-world` und `docker build` liefen. Die Maschine im selben Pod blieb ohne root, ohne Capabilities, mit Seccomp `RuntimeDefault`, `brew install docker` brachte den Client, `DOCKER_HOST=tcp://127.0.0.1:2375` reichte. Der Daemon braucht einige Sekunden nach dem Start des Pods (die Bereitschaftsprüfung des Pods muss auf ihn warten) |

**Folgen:**
1. Docker im Pod braucht heute einen **privilegierten Container**. Der Namespace braucht Pod Security `privileged`; ein kompromittierter Daemon ist Root auf dem Knoten. In k3d lokal ist das vertretbar, in geteilten Clustern nicht. Das Handbuch sagt es so.
2. Wer das nicht darf, nimmt `remote` (ein Daemon außerhalb) oder `none`. Langfristig sind Sysbox, Kata oder die Benutzer-Namensräume der Cluster der Weg ohne Privileg; das hängt vom Cluster ab und ist nicht Teil dieses Plans.
3. `K8S_DOCKER=dind` bestimmt auch den Sicherheitskontext des Pods und die Pod-Security-Stufe, auf die der Preflight prüft (§5 Punkt 7).

## 16 Verwaltete Cluster (AWS EKS, Google GKE, Azure AKS)

**Stand: nicht geprüft.** Es gibt hier kein Konto; alles in dieser Tabelle ist Wissen über die Dienste *[?]*, kein Test. Die Anforderung „Docker muss gehen“ (§15) und die Wahl `dind` gelten für k3d. In verwalteten Clustern gilt: `viper` selbst braucht dafür **nichts anderes als eine `kubeconfig`**; die Unterschiede liegen im Cluster.

**Was `viper` zusätzlich braucht (kleine Änderungen am Plan):**

| Bedarf | Warum | Änderung |
|---|---|---|
| **Anmeldung über ein Hilfsprogramm** | Die `kubeconfig` von EKS (`aws eks get-token`), GKE (`gke-gcloud-auth-plugin`) und AKS (`kubelogin`) ruft beim Verbinden ein Programm auf. `client-go` kann das (Exec-Plugins), das Programm muss auf dem Rechner des Nutzers installiert sein | keine; der Preflight meldet es, wenn die Anmeldung scheitert |
| **Image in einem Register des Clusters** | Der Cluster zieht das Image aus ECR, Artifact Registry oder ACR, nicht von deinem Rechner (`k3d image import` gibt es nicht); privat heißt `imagePullSecrets` | `K8S_IMAGE_PULL_SECRET` (Name eines vorhandenen Secrets, `viper` legt keines an); `make image` bekommt ein Ziel zum Hochladen (nutzt `docker push`, Anmeldung macht der Nutzer) |
| **Mehrere Architekturen** | AWS Graviton, GKE Axion und AKS Ampere sind arm64; x86 ist der Standard | Image für `amd64` und `arm64` (D7 war nur amd64) *[(zu bestätigen)]* |
| **Eine eigene Knotengruppe für `dind`** | Ein privilegierter Container auf gemeinsamen Knoten ist Root dort; Knotengruppe mit Taint, Pod mit Toleration | `K8S_NODE_SELECTOR` und `K8S_TOLERATION` (waren in §8 „später“) |
| **Datenträger-Klasse** | EKS braucht das EBS-CSI-Add-on für `gp3`, GKE hat `standard-rwo`, AKS `managed-csi`; ein Volume liegt in **einer Zone**, der Pod muss danach dort laufen | Preflight prüft, ob eine StorageClass da ist; `WaitForFirstConsumer` ist üblich; bei Ausfall der Zone bleibt der Pod hängen (Hinweis im Handbuch) |
| **Rechte der Anmeldung** | `pods/portforward`, Secrets, StatefulSets, PVC, bei `dind` privilegierte Pods | Preflight mit `SelfSubjectAccessReview` (§5 Punkt 7) |
| **Verbindung nicht abreißen lassen** | Der Port-Forward geht durch die API und meist durch einen Load Balancer mit Leerlaufgrenze (etwa AWS-NLB 350 Sekunden) | `ServerAliveInterval` in der SSH-Konfiguration, die `viper` schreibt |
| **Erreichbarkeit der API** | Private Cluster sind nur über VPN oder Bastion erreichbar | Preflight sagt, dass die API nicht antwortet; mehr nicht |
| **Internetzugang der Pods** | `brew`, Claude-Installer, Git-Klon brauchen ausgehende Verbindungen (NAT-Gateway) | Preflight kann es nicht prüfen; Handbuch |

**Was in den Diensten anders ist *[?]*:**

| | EKS | GKE | AKS |
|---|---|---|---|
| privilegierte Pods (`dind`) | nur auf EC2-Knoten, **nicht auf Fargate** | nur Standard-Cluster, **nicht Autopilot** | ja, auf normalen Knoten |
| NetworkPolicy (Gruppennetz bei NATS) | nur mit aktiviertem Dienst im VPC-CNI oder Calico | mit Dataplane V2 oder aktivierter Option | nur mit Azure NPM, Calico oder Cilium **beim Anlegen** gewählt |
| Standard-Anmeldung | `aws` CLI | `gcloud` mit Plugin | `az` mit `kubelogin` |
| Register | ECR | Artifact Registry | ACR |

Wo `dind` nicht erlaubt ist (Fargate, Autopilot, „restricted“-Namespaces), bleibt `K8S_DOCKER=remote` oder `none` (§15). Wo NetworkPolicy nicht durchgesetzt wird, gilt der NATS-Schutz des Ports nur durch die Schichten des Servers (Schlüssel, Zertifikat, Bindung an die Pod-IP), nicht durch das Netz; `viper check` kann das nicht erkennen, das Handbuch sagt es.

**Aussage über die Qualität:** In k3d getestet; EKS, GKE und AKS nur nach dem Plan gebaut, ein Lauf ist erst mit einem Konto belegt (wie bei AWS, Google Cloud und Azure als VM-Anbieter). Kosten entstehen durch die Knoten, nicht durch `viper`; ein Cluster ohne freie Knoten lässt den Pod in „Pending“ hängen, und der Preflight kann nur die vorhandene Kapazität nennen.

## 17 Ergebnis Meilenstein 1: das Image (Stand 12.11.2026)

Dateien: `image/Dockerfile`, `image/entry.sh`, `image/sshd_config`, `image/seed.py`, `image/test.sh`; `make image` und `make image-test`. Gebaut mit Ubuntu 26.04 (Digest festgelegt), 2 Minuten 22 Sekunden, **2,6 GB**. `image/test.sh` läuft mit **33 Prüfungen, alle grün**, unter den Einschränkungen von Pod Security „restricted“ (uid 1000, `--cap-drop ALL`, `no-new-privileges`), ohne Cluster; `shellcheck` meldet nichts.

* **Eine Quelle für VM und Image:** `seed.py` nimmt die Härtung von `sshd` (`01-keys-only.conf`), die `tmux`-Konfiguration und die VS-Code-Einstellungen aus `cloud-init.yaml`. Die Datei `image/sshd_config` bindet die Härtung ein und ergänzt nur Port 2222, Host-Schlüssel und -Zertifikat, `UsePAM no`, `StrictModes no` und SFTP. Die wirksame Konfiguration (`sshd -T`) wird geprüft: Passwort aus, Tastatur-Interaktiv aus, nur Schlüssel, kein Root, Port 2222.
* **Inhalt:** die Pakete aus `cloud-init.yaml` (ohne die, die nur eine VM braucht), `tini` als PID 1, Docker-Client mit Buildx und Compose (Schlüssel mit dem veröffentlichten Fingerabdruck geprüft, wie in `cloud-init.yaml`), Homebrew (im Image, `/home/linuxbrew`), Claude Code (Wiederholung mit wachsenden Pausen, wie bei der VM). `sudo` gibt es nicht.
* **Heimverzeichnis und Volume:** Der Inhalt des Heimverzeichnisses (Claude, `tmux`, VS Code) liegt zusätzlich unter `/opt/viper/home-seed`. Das Einstiegsprogramm kopiert ihn **einmal** in ein leeres Volume (`cp -an`, Markierung `~/.viper-seeded`) und überschreibt nichts. Geprüft mit einem leeren, für alle beschreibbaren Verzeichnis, wie ein Cluster es anlegt: Claude läuft daraus, und eine Datei im Volume überlebt einen Neustart des Containers. Die Schlüssel kommen nur als Dateien (`VIPER_SECRET_DIR`), nie als Umgebung oder Befehlszeile.
* **Bereitschaft:** `/tmp/viper-ready` (anpassbar mit `VIPER_READY_FILE`), geschrieben kurz vor dem Start von `sshd`; die Bereitschaftsprüfung des Pods soll zusätzlich Port 2222 prüfen.
* **Getestet auch:** Anmeldung mit Host-Zertifikat, falscher Host-Name abgelehnt, anderer Schlüssel und Passwort abgelehnt, `scp`, `brew install` ohne root, Docker-Client und Plugins.

**Offen / für später:**
* **Größe 2,6 GB.** Das meiste sind `build-essential`, Homebrew und Claude; kleiner wird es nur durch Weglassen (etwa `build-essential`, wenn Homebrew nur Bottles braucht). Für den ersten Schritt akzeptiert.
* Nur `amd64` (D7); `arm64` mit Meilenstein 6.
* `code-server` fehlt (nicht nötig für VS Code Remote-SSH, §15).
* Der Test läuft nicht in `make check`/CI (er braucht Docker und dauert Minuten); er ist ein eigenes Ziel.

### 17.1 Nachtrag: NATS im Image und Abgleich mit cloud-init

`nats-server` (v2.15.2) und das Werkzeug `nats` (v0.5.1) sind im Image, in `/usr/local/bin`, mit denselben Versionen und Prüfsummen wie auf einer VM (`amd64` und `arm64`, per `TARGETARCH`). Die Prüfsummen stehen im `Dockerfile` und im Go-Code (`internal/natsserver`); der Test `TestTheImagePinsTheSameNATSReleasesAsTheCode` schlägt fehl, wenn sie auseinanderlaufen. `make image-test` hat jetzt **38 Prüfungen, alle grün**, darunter: beide Versionen, ein `nats-server` als `builder` ohne Rechte, der dem `nats`-Werkzeug antwortet, und die Skripte `natsinstall.sh` und `natscli.sh` von `viper` laufen unverändert und melden „is installed“ (sie brauchen so kein `sudo`). Der MCP-Server `viper-nats-mcp` kommt wie auf einer VM von `viper up` nach `~/.viper-nats` (kein root nötig).

Abgleich mit dem, was `cloud-init.yaml` einrichtet:

| `cloud-init.yaml` | im Image |
|---|---|
| Pakete `gnupg git gh make build-essential curl ca-certificates tmux jq openssh-server psmisc procps file` | alle enthalten (zusätzlich `python3`, `tini`) |
| Docker Engine, containerd, Buildx, Compose | **Client**, Buildx und Compose; der Daemon ist der Sidecar oder ein entfernter (D5) |
| Benutzer `builder`, `sudo`, Gruppe `docker` | `builder` (uid 1000); **kein `sudo`**, keine Gruppe nötig |
| `sshd`-Härtung | dieselbe Datei, aus `cloud-init.yaml` übernommen (`seed.py`) |
| `.tmux.conf`, VS-Code-Einstellungen | dieselben Dateien, übernommen |
| Claude Code (`install.sh`) | enthalten |
| Updates, Neustart, Host-Schlüssel, Authorized Keys | entfallen bzw. kommen als Secret (§3) |
| `code-server` (von `viper up` installiert, nicht von cloud-init) | fehlt; VS Code Remote-SSH braucht ihn nicht |
| Homebrew (von `viper up` installiert) | enthalten |

## 18 Ergebnis Meilenstein 2: der Adapter (Stand 12.11.2026)

`internal/adapter/driven/kubernetes` (mit `client-go` v0.37.2), eingehängt als `PROVIDER=kubernetes` (`model/provider.go`, `app/provider.go`, `settingsfile`). Getestet gegen die Fake-Clientmenge von `client-go`, deren Reaktoren das nachbilden, worauf der Adapter sich verlässt (ein StatefulSet macht seinen Pod, das Löschen nimmt ihn weg). `go test ./...`, Linter und `gofmt` grün.

* **Anlegen:** Namespace (wenn er fehlt, mit Label `viper-managed`; bei `dind` mit Pod Security `privileged`; er wird nie gelöscht), Secret (`authorized_keys`, `host_key`, `host_key-cert.pub`), PVC, StatefulSet; wartet auf den bereiten Pod und bricht früher ab, wenn er nicht starten kann (`ImagePullBackOff`, `Unschedulable`). Ein Rest von Secret oder PVC eines halb gescheiterten `down` wird vorher entfernt, aber nur, was `viper` angelegt hat; ein fremdes Objekt gleichen Namens bricht mit `ErrNotOurs` ab.
* **Pod:** Maschine als uid 1000, ohne Capabilities, ohne Privilegieneskalation, Seccomp `RuntimeDefault`, ohne API-Zugang (`automountServiceAccountToken: false`), Größe `<cpu>x<GiB>` als Requests und Limits, Schlüssel nur als Datei (nie in Umgebung oder Befehlszeile; getestet). `K8S_DOCKER`: `dind` (privilegierter Sidecar, der Pod ist erst bereit, wenn der Daemon antwortet), `remote` (`DOCKER_HOST` aus `K8S_DOCKER_HOST`) oder `none`.
* **`Server.ID`** ist die UID des StatefulSet (§3). `Server.ViaAPI` und `Server.SSHPort` (2222) sagen den Aufrufern, dass es keine Adresse gibt.
* **Löschen:** StatefulSet (Vordergrund-Kaskade), Warten auf Pod, Secret, Volume; ein bereits entferntes ist kein Fehler.
* **Preflight:** Kontext und API, Größe, Image, Namespace samt Pod-Security-Stufe, Rechte (`SelfSubjectAccessReview` je Aufgabe), StorageClass, Docker-Modus, vorhandener Server.
* **Einstellungen (`settingsfile`):** `SERVER_LOCATION` (Kontext) und `K8S_IMAGE` sind Pflicht ohne Standard; `DNS_PROVIDER=none` ist Pflicht; `WORKSPACE_DIR` nur unter `/home/builder` (Standard `/home/builder/work`); `K8S_DOCKER` geprüft; `K8S_DISK_GB` 1 bis 16384; NATS bleibt verweigert, bis Meilenstein 5 fertig ist.
* **Arbeitsverzeichnis:** Standard `/home/builder/work`, auf dem Volume; das Image kennt keinen Pfad des Nutzers. Eine Einstellung `WORKSPACE_DIR` außerhalb von `/home/builder` wird für diesen Anbieter abgelehnt.

**Noch nicht angeschlossen** (Meilenstein 3): `start`, `stop`, `check` brauchen eine IPv4 und `cloud-init`; `viper k8s-proxy` und die SSH-Konfiguration mit `ProxyCommand` fehlen. Bis dahin scheitert `viper up` mit diesem Anbieter („has no IPv4 address“).

## 19 Ergebnis Meilenstein 3 und 4: Anbindung und echter Lauf in k3d (Stand 12.11.2026)

**Anbindung (M3):** `Server.ViaAPI` und `SSHPort`; `sshconfig.WriteConfig` schreibt für einen Pod `HostName <servername>`, `Port 2222` und einen `ProxyCommand` (`viper k8s-proxy --dir <einstellungen> <maschine>`, Pfade zitiert, `%` verdoppelt, Anführungszeichen, Zeilenumbrüche und Backslashes abgelehnt; unter Windows mit Schrägstrichen); `PinHostKey` liest den Schlüssel aus `/home/builder/.viper/sshd/host_key.pub`. In `start`, `stop` und `check` fallen die Annahmen einer VM weg (keine Adresse, kein DNS, keine Firewall, kein cloud-init, kein `sudo`, keine Auslagerungsdatei, kein `code-server`); die Probe für Pods meldet bereit, wenn die Bereitschaftsdatei da ist, Claude im Image liegt und Docker antwortet. `VIPER_SWAP` und `VIPER_CODE_SERVER` werden für diesen Anbieter abgelehnt. Das Image gibt `DOCKER_HOST` per `SetEnv` an die SSH-Sitzungen weiter. `viper k8s-proxy` (Port-Forward mit `client-go`) ist ein Unterbefehl von `viper`.

**Echter Lauf (M4)** in k3d v5.9.0 (k3s v1.35.5), Pod Security `privileged` im Namespace `viper`, Maschine `1x2`, Volume 2 GB, `dind`:

| Schritt | Ergebnis |
|---|---|
| `viper preflight` | grün; die Rechteprüfung (`SelfSubjectAccessReview`), StorageClass, Namespace, Größe, Image gegen den echten Cluster |
| `viper up` | **fertig in rund 25 Sekunden** nach dem Anlegen (Image und Docker-Image lagen im Cluster); SSH durch die API, Homebrew, Claude, Docker bereit, „Ready“ mit Arbeitsverzeichnis `/home/builder/work` |
| `viper check` | grün: sshd nur Schlüssel, Docker läuft, Homebrew, gh, Git |
| `viper ssh --cmd` | `DOCKER_HOST` gesetzt, `docker run hello-world` lief, Arbeitsverzeichnis beschreibbar |
| Pod ersetzt (`kubectl delete pod`, das StatefulSet legt einen neuen an) | Dateien im Arbeitsverzeichnis und im Heimverzeichnis blieben; `viper up` danach nahm den Pod wieder auf |
| `viper down` (mit und ohne `--force`) | StatefulSet, Pod, Secret und Volume entfernt, Namespace bleibt (mit `viper-managed` und Pod Security `privileged`) |

**Fehler, die der echte Lauf aufdeckte (alle behoben):**
1. Das Einstiegsprogramm brach ab, weil `cp -a` die Zeiten des Volume-Wurzelverzeichnisses setzen wollte, das der Cluster `root` zuweist. In meinem lokalen Test gehörte es dem Benutzer 1000. `image/test.sh` legt das Volume jetzt als `root` an, wie ein Cluster es tut (39 Prüfungen).
2. `ssh`-Konfiguration unter Windows: Pfade mit Backslashes wurden vom Quoting der `ProxyCommand` abgelehnt (CI); jetzt Schrägstriche.
3. Die Meldung „Existing firewall … verify that its rules permit your connection“ erschien auch bei Pods; der Firewall-Schritt entfällt für sie.
4. Ein Arbeitsverzeichnis `/Development` (der Standard der VMs, in einem globalen `.env.local` gesetzt) ist für Pods nicht möglich; der Standard dafür ist `/home/builder/work`, und die Meldung sagt, wo man `WORKSPACE_DIR` für die Maschine setzt.

**Bekannte Grenzen:**
* **Programme, die man mit `brew install` nachinstalliert, überleben das Ersetzen des Pods nicht**, weil Homebrew im Image liegt und nicht auf dem Volume. Was `VIPER_BREW_PACKAGES` nennt, wird bei jedem `viper up` wieder installiert. Ein zweites Volume für `/home/linuxbrew` wäre die Lösung (das Verzeichnis wäre beim ersten Start aus dem Image zu füllen) und ist nicht gebaut.
* `viper` ist durch `client-go` und den Port-Forward etwa **34 MiB** größer (117,5 statt 83,9 MiB, gestrippt).
* Die Arbeitsspeicher- und Plattenanzeige in `viper ssh` und `check` zeigt die Werte des Knotens, nicht die Grenzen des Pods.
* VS Code Remote-SSH über den Proxy ist nicht geprüft (keine grafische Oberfläche).

## 20 Ergebnis Meilenstein 5: NATS im Cluster, echter Lauf in k3d (Stand 12.11.2026)

`PROVIDER=kubernetes` mit `NATS_ENABLED`: Gruppe `kn` aus drei Pods (Server `kna`, Clients `knb` und `knc`, MCP-Server an), k3d v5.9.0.

**Umbau gegenüber den VMs (Details in `docs/maintainer/architektur.md`):** Gruppennetz = NetworkPolicy `viper-nats-<gruppe>` (Anlegen ist die Sperre) plus headless Service gleichen Namens; die Clients erreichen den Server über `viper-nats-<gruppe>.<namespace>.svc`, das Zertifikat lautet auf diesen Namen (`natscert.sh`: IP oder DNS), der Server lauscht auf `$POD_IP` (`host: $POD_IP`, `# viper-advertise` in `server.conf`, `natsserver.Config.Listen`), `nats-server` läuft als Prozess des Containers (`image/nats-supervisor.sh`; `natsproc.sh` statt `natsunit.sh`; `natsconfig.sh` lädt per `SIGHUP` über die PID-Datei neu; `natsstatus.sh` meldet Dienst aktiv über die PID). Labels der Gruppe und der Rolle liegen an der Pod-Vorlage (das Netz wählt danach), der Zustand nur am StatefulSet (eine Änderung der Vorlage würde den Pod neu starten).

| Schritt | Ergebnis |
|---|---|
| `viper up --group kn` | grün; Server in 36 s, Clients nach rund einer Minute; „NATS client … ready (server viper-nats-kn.viper.svc:4222)“ |
| `viper check --group kn` | grün: Zertifikat gültig, Anmeldung über TLS mit dem NKey, **der Server lauscht nur auf `10.42.0.x:4222`**, drei Maschinen angemeldet |
| `viper nats send/broadcast/request/listen` | Text unverändert (Anführungszeichen, `$(id)`, `{{…}}`, Zeilenumbruch); `listen --all` zeigt Direktnachrichten der anderen |
| `viper nats init`, senden als Betreiber | über den SSH-Tunnel durch den Proxy, TLS gegen den Dienstnamen; `viper check` zeigt den Betreiber an |
| **Server-Pod ersetzt** (neue IP) | `nats-server` startete auf der neuen Adresse aus der Konfiguration auf dem Volume; Clients, Betreiber, Zertifikat und `viper check` ohne neue Anmeldung |
| NetworkPolicy | ein Pod ohne Gruppenlabel erreicht den Port nicht, ein Pod der Gruppe schon |
| `viper down --group kn` | alles weg, auch Policy und Service; Hinweis auf den übrig gebliebenen Betreiber-Schlüssel |

**Fehler, die der echte Lauf aufdeckte (alle behoben):**
1. `nats-server --log_size_limit` auf der Kommandozeile macht **jedes** Neuladen ungültig („config reload not supported for LogSizeLimit“): die Schlüssel der Clients wurden nie gelesen, die Anmeldung scheiterte mit „Authorization Violation“. Das Image-Test prüft jetzt, dass das Neuladen im Log steht.
2. Dem Image fehlte `ss` (Paket `iproute2`), das die Skripte von `viper` brauchen.
3. `viper down --group` fragt nach `DISCARD`, wenn eine Maschine ihr Arbeitsverzeichnis nie bekam (hier: nach einer gescheiterten Anmeldung); das gilt auch bei `--force`.

**Meilenstein 6 (verwaltete Cluster):** ohne Konto nicht möglich. Vorhanden: der Preflight (Rechte mit `SelfSubjectAccessReview`, StorageClass, Pod-Security-Stufe des Namespace), `K8S_STORAGE_CLASS`, `K8S_NAMESPACE`, `K8S_DOCKER=remote|none`. Offen: `K8S_IMAGE_PULL_SECRET`, `K8S_NODE_SELECTOR`/`K8S_TOLERATION`, `arm64`-Image, `ServerAliveInterval` ist schon gesetzt. Das Handbuch sagt, dass EKS, GKE und AKS nicht in einem echten Konto getestet sind.

## 21 Nachtrag: Image für Benutzer und Volume für Homebrew (Stand 12.11.2026)

**Wie das Image in den Cluster kommt.** Vorher nur von Hand: `make image` und `k3d image import`; ein Benutzer mit dem `viper`-Binary hat weder `make` noch das Repository. Jetzt:
1. **`viper image build [--tag NAME:TAG] [--k3d CLUSTER]`** baut das Image mit Docker aus den in `viper` eingebetteten Dateien (`embed.go`: `Dockerfile`, Einstiegsprogramm, `sshd_config`, `seed.py`, Prozessverwalter, dazu `cloud-init.yaml`); mit `--k3d` kommt es in den Cluster. Standardtag `viper-machine:<version>`. Tag und Clustername werden geprüft (nichts, was Docker als Option läse).
2. **Workflow `image.yml`:** bei einem `v*`-Tag (oder von Hand für einen vorhandenen Tag) baut er das Image, führt `image/test.sh` aus und veröffentlicht `ghcr.io/<eigentümer>/viper-machine:<tag>` und `:latest`. Ein `LABEL org.opencontainers.image.source` verknüpft das Paket mit dem Repository. **Das erste veröffentlichte Paket ist privat und muss einmal öffentlich geschaltet werden.** Der Workflow ist nie gelaufen (er veröffentlicht erst beim nächsten Tag). Ein Standard-`K8S_IMAGE` auf dieses Image gibt es **noch nicht**: erst nach der ersten Veröffentlichung, sonst zeigte er ins Leere.
3. Andere Cluster: `docker push` in ein Register und `K8S_IMAGE` darauf.

**Homebrew auf einem eigenen Volume.** `/home/linuxbrew` ist ein zweiter PVC (`<name>-brew`, `K8S_BREW_GB`, Standard 10): Das Image legt Homebrew (220 MB) nach `/opt/viper/brew-seed`, das Einstiegsprogramm füllt das Volume beim ersten Start (ohne Volume landet es im Container, wie vorher); `viper down` löscht beide Volumes. Getestet im Image mit einem Volume, das `root` gehört wie im Cluster: `brew install hello`, neuer Container, `hello` läuft noch (`image/test.sh`, 46 Prüfungen). **Im Cluster geprüft** (k3d): beide Volumes angelegt, nach dem Ersetzen des Pods laufen `brew` und das mit `brew install` nachinstallierte `hello` weiter.

**Git-Repository:** liegt unter `WORKSPACE_DIR` im Heimverzeichnis und damit auf dem Volume `<name>-home`. Im Cluster geprüft: `viper up --repo` klonte es nach `/home/builder/work/hetzner-viper`; nach dem Ersetzen des Pods war der Klon samt einer lokalen, nicht versionierten Datei noch da. Der ganze Weg eines Benutzers lief: `viper image build --k3d`, `viper up --repo`, `brew install`, neuer Pod, `viper down` (fragt nach `DISCARD` wegen der lokalen Datei).

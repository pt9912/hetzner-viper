# Plan: Kubernetes als Anbieter (Container statt Maschinen), zuerst mit k3d

**Ziel:** `PROVIDER=kubernetes` startet eine „Maschine“ als **Pod** in einem beliebigen Kubernetes-Cluster (k3d lokal, aber auch GKE, EKS, AKS, Hetzner-Cluster), statt als VM bei einem Cloud-Anbieter. `viper up`, `down`, `check`, `ssh`, `vscode` und die Gruppen sollen dort so arbeiten wie bei den anderen Anbietern. Der Anbieter ist der **Cluster**, nicht die Cloud darunter: `viper` braucht nur eine `kubeconfig`.

**Stand:** Entwurf, einmal unabhängig gegen den Code geprüft; die Befunde sind eingearbeitet (§12) und ändern den Plan an mehreren Stellen (die „Naht“ SSH, die Server-ID, NATS-Labels, Docker, Sicherheitskontext, `ProxyCommand`). Meilenstein 0 ist teilweise gelaufen (§13); sonst ist nichts umgesetzt.

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

Das Image (Dockerfile im Repository, `make image`): Ubuntu, die Pakete aus `cloud-init.yaml`, Benutzer `builder`, `sshd` mit derselben Härtung (die Datei `01-keys-only.conf` kommt aus **einer** Quelle für VM und Image; ein Test prüft die wirksame Konfiguration mit `sshd -T`), `tini`/`s6` als PID 1, Sicherheitskontext für `sshd` (läuft als root mit `CAP_SYS_CHROOT`, `CAP_SETUID`, `CAP_SETGID`; `builder` bekommt `sudo`, wofür `allowPrivilegeEscalation` nötig ist — Cluster mit Pod Security „restricted“ lehnen das ab, der Plan nennt das als Grenze), ein Einstiegsprogramm, das das Secret einliest, die Host-Schlüssel mit Zertifikat ablegt und `sshd` startet, und `/run/viper-ready`, sobald es fertig ist. nats-server und `nats`-Werkzeug sind schon im Image (statt `natsinstall.sh`).

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
* Docker-in-Docker: nicht im ersten Schritt (D5). Cluster mit Pod Security „restricted“: werden nicht unterstützt, solange `sshd` und `sudo` privilegierte Rechte brauchen (§3).
* Preise in `viper find`.

## 9 Entscheidungen

D1 **Zugang:** `sshd` im Pod und `ProxyCommand` (Empfehlung) statt `kubectl exec` in jedem Skript *[Empfehlung, Begründung §2 Regel 1]*.
D2 **StatefulSet** (Empfehlung) statt Pod: stabiler Name, Volume gebunden, Neustart durch den Cluster.
D3 **Volume:** ein PVC für `/home/builder` und das Arbeitsverzeichnis, von `viper` selbst angelegt und mit `down` ausdrücklich gelöscht (Empfehlung; es folgt dem heutigen „alle Daten werden zerstört“). Das Einstiegsprogramm legt bei leerem Volume die Dateien des Heimverzeichnisses an.
D4 **`client-go` und `viper k8s-proxy`** (Empfehlung) statt `kubectl`.
D5 **Docker auf der Maschine:** zunächst aus (`VIPER_DOCKER=0`, Standard nur bei diesem Anbieter; die Einstellung gibt es noch nicht, §5 Punkt 3); Docker-in-Docker später, wenn gewünscht.
D6 **NATS:** als letzter Meilenstein, nicht Teil der ersten Abnahme.
D7 **Image-Verteilung:** `make image` und `k3d image import`; für andere Cluster ein Register der Wahl in `K8S_IMAGE`.
D8 **Namen der Einstellungen:** `K8S_*` (zu bestätigen).

## 10 Meilensteine

| # | Titel | Inhalt |
|---|---|---|
| 0 | Verifikation | **1.** `k3d` installieren (`kubectl` ist da), Cluster mit dem fertigen k3s-Image anlegen, in dieser Umgebung erreichbar? **2.** `sshd` ohne `systemd` im Container mit Host-Zertifikat und `AuthenticationMethods publickey`. **3.** `ProxyCommand` mit Port-Forward: `ssh`, `scp`, VS Code Remote-SSH. **4.** NetworkPolicy-Anlegen als atomare Sperre (zwei gleichzeitige Aufrufe); Durchsetzung der Policy in k3d. **5.** Daten bleiben nach Neustart des Pods; ein leeres Volume wird mit den Dateien des Heimverzeichnisses angelegt; `down` hinterlässt keinen PVC. **6.** `sudo` und `sshd` mit den nötigen Rechten, `code-server`/`systemctl`-Ersatz, Claude Code und Homebrew im Image. **7.** Speicher: k3d plus Pod in 4 GB RAM. **8.** Entscheidungen aus §9. |
| 1 | Image | Dockerfile, Einstiegsprogramm, `make image`, Test mit `docker run` |
| 2 | Adapter | `kubernetes`-Adapter gegen die Fake-API: Anbieter ohne NATS |
| 3 | Anbindung | `ServerSpec`, `Server.Proxy`, `WriteConfig` mit ganzer `Server`-Angabe und alle Stellen aus §5 Punkt 4, „Container“-Fähigkeit in `start`/`check` (§5 Punkt 3), `K8S_*` in `settingsfile`, `viper k8s-proxy`, Quoting der SSH-Konfiguration |
| 4 | Echter Lauf | k3d: `up`, `check`, `ssh`, Neustart, `down`; Handbuch (Kapitel zum Anbieter) |
| 5 | NATS | Dienstname und Zertifikat, Prozessverwalter, NetworkPolicy als Gruppennetz; Lauf mit drei Pods |

## 11 Definition of Done

* Meilenstein 4 mit echtem Lauf in k3d, `go test -race ./...` und der Linter grün, `gofmt` geprüft.
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

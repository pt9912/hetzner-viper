# Plan: NATS-Gruppen bei AWS, Google Cloud und Azure

**Ziel:** `NATS_ENABLED` soll nicht nur mit `PROVIDER=hetzner` gehen, sondern auch mit `aws`, `gcp` und `azure`. Alles, was auf einer NATS-Gruppe aufbaut (MCP-Server, Gruppendateien, `viper nats`), soll dort gleich funktionieren.

Grundlage ist [`nats.md`](nats.md) (Gruppe, Rechte, Server-Bootstrap). Dieser Plan ändert an der Gruppenlogik nichts, er ergänzt nur, was ein Anbieter dafür können muss. Die Fortsetzung für die Kommandozeile ist [`nats-cli.md`](nats-cli.md).

**Stand:** Entwurf, ungeprüft. Es ist noch nichts umgesetzt.

**Kennzeichnung:** *[V]* heißt, im Code oder in einem Plan bestätigt. *[?]* heißt Vermutung; jede solche Stelle wird in Meilenstein 0 geprüft, bevor etwas darauf gebaut wird. **(zu bestätigen)** markiert Arbeitsannahmen, die eine Entscheidung brauchen.

## 1 Ausgangslage

* Die Gruppenlogik ist anbieterneutral gebaut. Der Anbieter liefert sie über die **optionale Fähigkeit** `cloud.NATSProvider` (`port/driven/cloud/nats.go`), die die Use Cases mit `cloud.AsNATS` suchen *[V]*. Nur der Hetzner-Adapter erfüllt sie *[V, `adapter/driven/hetzner/nats.go`, `network.go`]*.
* `ServerSpec` hat schon die neutralen Felder `Labels` und `Network` *[V, `model/model.go`]*. Beim Anlegen kann der Server also schon Metadaten und ein Netzwerk bekommen.
* Zwei harte Sperren: `settingsfile/config.go:152` lehnt `NATS_ENABLED` mit einem anderen Anbieter als Hetzner ab, und `start/nats.go:48` meldet „NATS groups are not supported with …“ *[V]*.
* **Was die Fähigkeit können muss** *[V, `NATSProvider`]*: die Server einer Gruppe auflisten (mit Rolle und Zustand), Rolle und Zustand eines Servers lesen und schreiben, eine Netzwerkzone je Standort nennen, ein **privates Netzwerk der Gruppe anlegen oder finden** (wobei das Anlegen die **Sperre des ersten Starts** ist: genau einer von mehreren gleichzeitigen Aufrufern bekommt `created=true` und wird Server-Kandidat), einen vorhandenen Server anbinden und die private IP nennen, und das Netzwerk löschen (`ErrNetworkInUse`, solange Server angebunden sind).
* Bei Hetzner sind das Netzwerk, der Name `viper-nats-<gruppe>` (eindeutig je Projekt) und die Labels echte Anbieterobjekte. Bei den anderen Anbietern sind die Begriffe anders; das ist die eigentliche Arbeit dieses Plans.
* Was die Anbieter heute schon haben *[V, Adapter]*:

| | Netz heute | Metadaten an der Maschine | Firewall heute |
|---|---|---|---|
| AWS | Subnetz: `AWS_SUBNET_ID`, sonst Standard-Subnetz (`network.go`) | Tags `viper=<name>` und `AWS_TAGS` an Instanz und Volume | eine Security Group je Rechnername (SSH) |
| Google Cloud | VPC `GCP_NETWORK` (Standard `default`), `GCP_SUBNET` | Netzwerk-Tag = Name der Firewall | Firewall-Regeln |
| Azure | VNet `viper-vnet`, Subnetz `viper-subnet` in der Ressourcengruppe | Tags `viper=<name>` | NSG |

* **Echte Läufe:** AWS ist in einem echten Konto getestet (`e2e`-Tests), Google Cloud und Azure nur gegen nachgebildete Schnittstellen *[V, Handbuch, `architektur.md`]*. Das bestimmt die Reihenfolge (§6) und die Aussage über die Qualität (§7).

## 2 Grundregeln

1. **Kein fremdes Netzwerk wird verändert** *[V, `nats.md` §4]*. Es entsteht kein neues VPC, VNet oder Subnetz. Die Gruppe nutzt das Netz, in das `viper` die Server ohnehin legt (Subnetz bzw. VPC aus den Einstellungen). Neu angelegt wird nur, was `viper` selbst benennen und wieder löschen kann: eine **Regel** oder **Gruppe von Regeln** für den NATS-Port.
2. **Alle Maschinen einer Gruppe liegen in einem Netz, in dem sie sich über private Adressen erreichen** (eine Region, ein VPC, ein VNet). Ein Gruppenmitglied in einem anderen Netz oder einer anderen Region wird mit klarer Meldung abgelehnt, bevor etwas angelegt wird.
3. **Der NATS-Port wird nur für die Gruppe geöffnet,** nie für das Internet *[V, `nats.md` §4]*. Der Server lauscht weiter nur auf der privaten IP *[V]*. Die Regel erlaubt `NATS_PORT` nur von Mitgliedern der Gruppe.
4. **Die Sperre des ersten Starts muss je Anbieter ein atomares „Anlegen mit eindeutigem Namen“ sein** (§3). Gibt es das nicht, ist der Anbieter für NATS nicht geeignet. „Erst suchen, dann anlegen“ ist keine Sperre *[V, `nats.md` §4]*.
5. **Die Labels der Gruppe gelten, nicht `NATS_ENABLED`** *[V, `architektur.md`]*. Der Anbieter muss Rolle und Zustand zuverlässig lesen und schreiben können, auch gleichzeitig von mehreren Maschinen.
6. Alles, was `viper` anlegt, trägt die Markierung `viper-managed=true` und die Gruppe, und `viper` löscht nur, was diese Markierung trägt.

## 3 Abbildung auf die Anbieter

### 3.1 Die Sperre des ersten Starts

| Anbieter | Eindeutiges Objekt | Wirkt als Sperre? |
|---|---|---|
| Hetzner | Netzwerk `viper-nats-<g>` | ja *[V]* |
| AWS | Security Group `viper-nats-<g>` im VPC (Namen sind je VPC eindeutig; ein zweites Anlegen liefert `InvalidGroup.Duplicate`) | ja *[?, in M0 belegen]* |
| Google Cloud | Firewall-Regel `viper-nats-<g>` (Namen sind je Projekt eindeutig; ein zweites Anlegen liefert `alreadyExists`) | ja *[?]* |
| Azure | NSG oder ASG mit festem Namen in der Ressourcengruppe | **nein ohne Zusatz:** `CreateOrUpdate` ist idempotent und meldet keinen Konflikt. Bedingtes Anlegen (`If-None-Match: *`) ist nur bei einem Teil der Ressourcen belegt *[?]* |

Azure ist damit der unsichere Fall. Ausweg, falls bedingtes Anlegen fehlt: eine **Sperre über einen anderen Weg**, zum Beispiel ein Blob/Lease oder ein eindeutiger Ressourcenname, bei dem das Anlegen scheitert (Ressourcengruppen-Sperre, Tag mit Etag-Vergleich). Das ist in M0 zu klären, bevor Azure beginnt.

### 3.2 AWS

* **„Netzwerk der Gruppe“** = das VPC des gewählten Subnetzes plus eine Security Group `viper-nats-<g>` mit einer Regel `TCP <NATS_PORT>` aus der **eigenen Gruppe** (self-referencing). `PrivateNetwork.ID` = ID der Security Group, `Zone` = Availability Zone bzw. Region (Server in verschiedenen AZ desselben VPC sind erlaubt; verschiedene Regionen nicht).
* **Anlegen/Sperre:** `EnsurePrivateNetwork` legt die Gruppe an (mit `AWS_TAGS_SHARED` und `viper-managed`/Gruppe, damit Tag-Richtlinien nicht verweigern; das Handbuch beschreibt dieses Verhalten für Schlüsselpaar und Security Group *[V, README]*) und liefert `created=true`; bei `InvalidGroup.Duplicate` wird die vorhandene gefunden und geprüft (eigene Markierung, gleiches VPC).
* **Anbinden:** nicht nötig, der Server liegt schon im VPC. `AttachPrivateNetwork` fügt die Security Group `viper-nats-<g>` zu den Gruppen der Instanz hinzu (`ModifyInstanceAttribute`) und liefert `PrivateIpAddress`. Beim Anlegen kommt sie über `ServerSpec.Network` gleich mit (heute `Groups: []string{l.group}`; die zweite Gruppe wird ergänzt).
* **Metadaten:** Tags an der Instanz (`viper-managed`, `viper-nats-group`, `-role`, `-state`). `UpdateNATSMetadata` ändert Tags mit `CreateTags` (ergänzt/überschreibt nur diese vier) *[?]*; `ListGroupMachines` nutzt `DescribeInstances` mit Tag-Filter und überspringt beendete Instanzen.
* **Löschen:** `DeletePrivateNetwork` löscht die Security Group; `DependencyViolation` wird zu `ErrNetworkInUse`.
* **Rechte (IAM):** zusätzlich `ec2:CreateSecurityGroup`, `AuthorizeSecurityGroupIngress`, `ModifyInstanceAttribute`, `CreateTags`, `DeleteSecurityGroup`, `DescribeInstances`/`DescribeSecurityGroups` (teils schon nötig). Der Preflight meldet fehlende Rechte vor dem Anlegen *[?, wie prüft er das heute?]*.

### 3.3 Google Cloud

* **„Netzwerk der Gruppe“** = die VPC `GCP_NETWORK` plus eine Firewall-Regel `viper-nats-<g>` (Eingang, `tcp:<NATS_PORT>`, Quelle und Ziel: das Netzwerk-Tag `viper-nats-<g>`). `PrivateNetwork.ID` = Regelname, `Zone` = Region.
* **Sperre:** Anlegen der Regel; bei `alreadyExists` wird die Regel gelesen und geprüft (Markierung in der Beschreibung, gleiche Parameter). Firewall-Regeln tragen keine Labels; die Markierung steht in der **Beschreibung** *[?]*.
* **Anbinden:** das Netzwerk-Tag `viper-nats-<g>` an die Instanz (`setTags`, braucht den Fingerprint) bzw. beim Anlegen über `ServerSpec.Network`. Private IP = `networkInterfaces[0].networkIP`.
* **Metadaten:** **Labels** der Instanz (`setLabels` mit `labelFingerprint`). Beschränkungen: Kleinbuchstaben, Ziffern, `-`, `_`, höchstens 63 Zeichen, höchstens 64 Labels; die vier `viper`-Labels passen *[?, Namen `viper-nats-group` etc. sind erlaubt]*. Gleichzeitige Änderungen verlangen eine Wiederholung bei `412` (Fingerprint).
* **Löschen:** die Regel löschen, wenn keine Instanz mit dem Tag mehr existiert (GCP kennt kein „in Gebrauch“; `viper` zählt die Mitglieder selbst und gibt sonst `ErrNetworkInUse`).
* **Rechte:** `compute.firewalls.create/delete/get`, `compute.instances.setTags/setLabels`.

### 3.4 Azure

* **„Netzwerk der Gruppe“** = das VNet `viper-vnet` mit dem Subnetz plus eine **Application Security Group** `viper-nats-<g>` und eine NSG-Regel `TCP <NATS_PORT>` mit Quelle und Ziel dieser ASG. (Alternative: nur die NSG-Regel mit dem Adressbereich des Subnetzes. Das öffnet den Port für das ganze Subnetz, nicht für die Gruppe; deshalb die ASG *[?, prüfen, dass ASG und NSG in der Ressourcengruppe so zusammenarbeiten]*.) `PrivateNetwork.ID` = ID der ASG.
* **Sperre:** siehe §3.1, offen.
* **Anbinden:** die NIC der VM bekommt die ASG (`ApplicationSecurityGroups` der IP-Konfiguration; nachträglich per Update der NIC). Private IP = die der NIC.
* **Metadaten:** Tags der VM (`viper`-Tags; Azure kennt bis zu 50 Tags, Werte bis 256 Zeichen); ein Update der Tags ersetzt je nach Aufruf alle oder fügt zusammen (`Update` mit `Merge`-Operation über `Tags.UpdateAtScope`) *[?]*.
* **Löschen:** ASG löschen; gelöscht werden kann sie erst, wenn keine NIC sie nutzt (Fehler → `ErrNetworkInUse`).
* **Rechte (RBAC):** `Microsoft.Network/applicationSecurityGroups/*`, `networkSecurityGroups/securityRules/*`, `networkInterfaces/write`, `Microsoft.Resources/tags/write`.

## 4 Änderungen am vorhandenen Code

1. **Sperren öffnen:** die Prüfung in `settingsfile/config.go:152` ersetzen. NATS ist erlaubt für jeden Anbieter, dessen Adapter `NATSProvider` erfüllt; sonst die bisherige Meldung. Die Prüfung beim Laden kann den Adapter nicht fragen (Einstellungen kennen keine Adapter); sie prüft gegen eine Liste der Anbieter, die in der Anbietertabelle (`model/provider.go`) als `NATS: true` stehen *[?, wo passt das besser hin]*. Der Test `start/nats.go:48` bleibt als zweite Sicherung.
2. **Fachliche Begriffe klarstellen:** `PrivateNetwork` heißt bei AWS/GCP/Azure „Verbindungsregel der Gruppe“. Die Schnittstelle bleibt, die Kommentare in `port/driven/cloud/nats.go` sagen es (kein Umbenennen, damit der Hetzner-Teil unverändert bleibt).
3. **`NetworkZone`** wird je Anbieter belegt: AWS/GCP/Azure geben die Region zurück; die Prüfung „alle Server einer Gruppe in einer Zone“ bleibt in `start`.
4. **Private IP:** `model.Server.PrivateIP` wird von den Adaptern in `ServerByName`/Listen gefüllt (bei Hetzner aus dem angebundenen Netz) *[V, Feld vorhanden]*.
5. **Server anlegen:** `ServerSpec.Labels` und `ServerSpec.Network` werden in den drei Adaptern ausgewertet (AWS: Tags und zweite Security Group; GCP: Labels und Netzwerk-Tag; Azure: Tags und ASG an der NIC).
6. **`viper check`:** die Prüfung „Port vom Internet aus nicht erreichbar“ ist anbieterneutral (von außen) *[V]*; die Prüfungen aus der Sicht des Anbieters (Gruppe, Rolle, Zustand, Netzwerk) laufen über `NATSProvider` und gelten dann auch für die neuen Adapter. Bei den neuen Anbietern kommt hinzu: die Verbindungsregel existiert und enthält die erwarteten Parameter.
7. **Preflight:** meldet für `NATS_ENABLED` fehlende Rechte (Liste je Anbieter, §3) und eine Regel `viper-nats-<g>`, die nicht von `viper` stammt.
8. **`down`:** nach dem letzten Mitglied wird die Verbindungsregel gelöscht (wie bei Hetzner das Netzwerk).
9. **Handbuch:** das Kapitel „Rechner über NATS verbinden“ nennt die unterstützten Anbieter und die Besonderheiten je Anbieter; der Satz „Andere Anbieter unterstützen NATS-Gruppen noch nicht“ entfällt anbieterweise erst dann, wenn der Anbieter belegt ist (§7).

## 5 Offene Fragen und Risiken

1. **Gleichzeitiges Schreiben der Rolle/des Zustands** (mehrere Maschinen setzen Labels): Hetzner liest zusammen und schreibt zusammen *[V]*. Bei GCP verlangt `setLabels` den Fingerprint (Wiederholung bei `412`), bei AWS ist `CreateTags` je Schlüssel atomar, bei Azure ist `Merge` zu belegen. Ein Test mit gleichzeitigen Schreibern je Adapter ist Pflicht.
2. **Die Auswahl der Server-Maschine hängt von der Sperre ab** (§3.1). Fehlt sie bei Azure, bleibt Azure zunächst ausgeschlossen.
3. **Private Erreichbarkeit nicht überall gegeben:** AWS `AWS_SUBNET_ID` auf ein Subnetz ohne Routen zwischen den Servern, GCP-VPC mit Regeln, die allen Verkehr verbieten, Azure-NSG-Richtlinien. Der Verbindungstest nach dem Start (`natsconnect.sh`) meldet das mit einer klaren Ursache; der Plan fügt keine Netzwerkdiagnose hinzu.
4. **Kosten:** Verkehr zwischen Availability Zones/Regionen kostet bei den Anbietern etwas. Das Handbuch weist darauf hin. Es entstehen keine NAT-Gateways oder eigene Netzwerke, also keine festen Kosten.
5. **Tag-Richtlinien und Richtlinien allgemein** (AWS `aws:RequestTag`, Azure Policy): wie bei den vorhandenen Ressourcen werden Tags beim Anlegen mitgegeben *[V, README zu `AWS_TAGS_SHARED`]*; für Azure/GCP gibt es kein Gegenstück zu `AWS_TAGS_SHARED`. Ob eine Einstellung dafür nötig ist, ist offen **(zu bestätigen)**.
6. **Region/Standort:** Hetzner nennt einen Standort (`fsn1`) und leitet die Zone ab. Bei AWS/GCP/Azure ist es die Region aus den Einstellungen (`SERVER_LOCATION`, `GCP_*`, `AZURE_*`); eine Gruppe über Regionen hinweg ist ausgeschlossen (§2, Regel 2).
7. **Zertifikat mit der privaten IP als SAN:** Bei Instanzen, die ihre private IP bei einem Neustart behalten (AWS, GCP: ja im Normalfall; Azure: dynamisch, aber stabil solange die VM besteht) ist es unproblematisch; beim Stoppen und Starten (AWS) bleibt die private IP, bei GCP ebenfalls, bei Azure ist sie bei „Deallocate“ nicht garantiert *[?]*. `check` meldet die Abweichung (IP im Zertifikat ≠ IP laut API) *[V]*.

## 6 Meilensteine

| Nr. | Meilenstein | Ergebnis |
|---|---|---|
| 0 | Verifikation | **1.** Atomares Anlegen je Anbieter (AWS `InvalidGroup.Duplicate`, GCP `alreadyExists`, Azure bedingtes Anlegen oder Ersatz), mit je zwei gleichzeitigen Aufrufen gegen ein echtes Konto (AWS) bzw. dokumentiert und gegen nachgebildete Schnittstellen (GCP, Azure). **2.** Gleichzeitiges Schreiben der Metadaten (AWS `CreateTags`, GCP `setLabels`, Azure `Merge`). **3.** Reicht eine ASG-Regel für „nur die Gruppe“ (Azure), und gibt es eine bedingte Anlage für die ASG? **4.** Entscheidungen aus §9. |
| 1 | Grundlage | Sperre in den Einstellungen öffnen (Anbietertabelle), Kommentare der Schnittstelle, Fake in `servertest` auf mehrere Anbieter ausrichten, Tests für „Anbieter ohne Fähigkeit“ bleiben |
| 2 | AWS | Adapter `NATSProvider` für AWS: Security Group als Verbindungsregel, Tags, private IP, Löschen; Unit-Tests gegen die nachgebildete API; **echter Lauf** mit zwei Instanzen (wie die vorhandenen `e2e`-Tests, Build-Tag `e2e`); Preflight-Rechte; Handbuch AWS |
| 3 | Google Cloud | wie AWS, ohne echten Lauf (siehe §7) |
| 4 | Azure | wie AWS, ohne echten Lauf; nur wenn M0 eine Sperre gefunden hat |
| 5 | Abnahme | Handbuch, Maintainer-Doku (`architektur.md`: eine Tabelle der Fähigkeiten je Anbieter), `viper check` je Anbieter; die Mitteilung zum Stand je Anbieter im Handbuch |

## 7 Teststrategie und Aussage über die Qualität

* **Unit:** je Adapter gegen die nachgebildete API (wie die vorhandenen Adaptertests): Anlegen mit Duplikat, Anbinden, Lesen der Metadaten, Löschen mit Mitgliedern, Labels/Tags zusammenführen, Fingerprint-Wiederholung (GCP).
* **Use-Case-Tests:** laufen gegen den Fake in `servertest/nats.go` und gelten für alle Anbieter; ein zusätzlicher Satz prüft die anbieterspezifischen Meldungen („NATS groups are not supported with …“ nur noch für Anbieter ohne Fähigkeit).
* **Gleichzeitigkeit:** je Adapter ein Test mit mehreren Aufrufern, die dieselbe Gruppe anlegen: genau einer bekommt `created=true`.
* **Echt:** AWS mit zwei Instanzen, `up --group` und `down --group` mit der Gruppe `entwicklung`-ähnlichen Namen, danach prüfen, dass die Security Group und alle Instanzen weg sind (die vorhandenen `e2e`-Tests räumen auch bei Fehlern auf *[V]*). **Für Google Cloud und Azure gibt es keinen echten Lauf** (kein Test-Konto im Projekt *[V, Handbuch]*). Das Handbuch sagt deshalb dort wörtlich: „gebaut und gegen nachgebildete Schnittstellen getestet, ein Lauf in einem echten Konto ist nicht belegt“, wie schon bei den Anbietern selbst. Wer ein Konto hat, kann die `e2e`-Tests für diese Anbieter ausführen; sie werden mitgeliefert, laufen aber nicht von selbst.
* **Regression:** alle bestehenden Tests; eine Hetzner-Gruppe verhält sich byte-identisch wie vorher (die Referenzdatei der Konfiguration, die Reihenfolge der Aufrufe).

## 8 Bewusst nicht enthalten

* Neue VPCs, VNets, Subnetze, Peerings, NAT, VPN oder private Endpunkte. Wer die Maschinen in verschiedenen Netzen hat, nutzt keine NATS-Gruppe.
* Gruppen über Regionen oder Anbieter hinweg (zum Beispiel ein Server bei Hetzner, Clients bei AWS).
* Andere Anbieter als AWS, Google Cloud und Azure.
* Änderungen an `nats.md` (Rechte, Zertifikat, Bootstrap).
* Eine Automatik, die vorhandene Regeln des Kunden ändert; was `viper` nicht selbst angelegt hat, wird nur gelesen.

## 9 Offene Entscheidungen

1. **Reihenfolge:** AWS, dann Google Cloud, dann Azure (Vorschlag: AWS zuerst, weil nur dort ein echter Lauf möglich ist), oder alle drei gleichzeitig?
2. **Azure:** bedingtes Anlegen nicht belegt → Azure zurückstellen, bis M0 eine Sperre gezeigt hat, oder mit einem eigenen Sperrmechanismus bauen?
3. **Wie mit Google Cloud und Azure ohne echten Lauf umgehen:** ausliefern mit klarer Aussage im Handbuch (Vorschlag), oder erst, wenn jemand einen echten Lauf gemacht hat?
4. **Einstellung für Tags/Labels:** ein Gegenstück zu `AWS_TAGS_SHARED` für GCP/Azure (zusätzliche Labels/Tags an den Gruppenressourcen)?
5. **Anbietertabelle:** `NATS`-Flag in `model/provider.go` (Vorschlag) oder die Prüfung erst beim Start?

## 10 Definition of Done

1. Für jeden gelieferten Anbieter: `up --group` einer neuen NATS-Gruppe legt Server und Clients an, die Clients melden sich beim Server über private Adressen, `check` meldet alles in Ordnung, `down --group` löscht Maschinen und Verbindungsregel.
2. Zwei gleichzeitige `up` derselben neuen Gruppe ergeben genau einen Server (belegt je Anbieter: AWS real, die anderen gegen die nachgebildete API).
3. Der NATS-Port ist vom Internet aus nicht erreichbar und im privaten Netz nur für die Gruppe offen (geprüft von außen und durch die Regel).
4. Ein Anbieter ohne Fähigkeit meldet weiter klar, dass er sie nicht hat; Hetzner verhält sich unverändert.
5. Kein fremdes Netzwerk und keine Regel, die `viper` nicht angelegt hat, wird geändert oder gelöscht.
6. Das Handbuch nennt je Anbieter, was getestet ist und was nicht, und welche Rechte er braucht.

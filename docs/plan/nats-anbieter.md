# Plan: NATS-Gruppen bei AWS, Google Cloud und Azure

**Ziel:** `NATS_ENABLED` soll nicht nur mit `PROVIDER=hetzner` gehen, sondern auch mit `aws`, `gcp` und `azure`. Alles, was auf einer NATS-Gruppe aufbaut (MCP-Server, Gruppendateien, `viper nats`), soll dort gleich funktionieren.

Grundlage ist [`nats.md`](nats.md) (Gruppe, Rechte, Server-Bootstrap). Dieser Plan ändert an der Gruppenlogik nichts, er ergänzt nur, was ein Anbieter dafür können muss. Die Fortsetzung für die Kommandozeile ist [`nats-cli.md`](nats-cli.md).

**Stand:** Entwurf, einmal unabhängig geprüft; die Befunde sind eingearbeitet (§11) und ändern den Plan an mehreren Stellen (Anbinden, AWS-Sperre, Sicherheitsaussage, Azure). Entschieden ist die Reihenfolge (§9). Umgesetzt: noch nichts.

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
| AWS | Subnetz: `AWS_SUBNET_ID`, sonst Standard-Subnetz (`network.go`) | Tags `viper=<name>` und `AWS_TAGS` an Instanz und Volume | eine Security Group, Name aus `SERVER_PREFIX` (`<prefix>-ssh`, für alle Rechner mit diesem Präfix gemeinsam; SSH) |
| Google Cloud | VPC `GCP_NETWORK` (Standard `default`), `GCP_SUBNET` | Netzwerk-Tag = Name der Firewall | Firewall-Regeln |
| Azure | VNet `viper-vnet`, Subnetz `viper-subnet` in der Ressourcengruppe | Tags `viper=<name>` | eine NSG `<prefix>-ssh`, gemeinsam für alle Rechner mit diesem Präfix; jede NIC hat genau diese eine NSG |

* **Echte Läufe:** AWS ist in einem echten Konto getestet (`e2e`-Tests), Google Cloud und Azure nur gegen nachgebildete Schnittstellen *[V, Handbuch, `architektur.md`]*. Das bestimmt die Reihenfolge (§6) und die Aussage über die Qualität (§7).

## 2 Grundregeln

1. **Kein fremdes Netzwerk wird verändert** *[V, `nats.md` §4]*. Es entsteht kein neues VPC, VNet oder Subnetz. Die Gruppe nutzt das Netz, in das `viper` die Server ohnehin legt (Subnetz bzw. VPC aus den Einstellungen). Neu angelegt wird nur, was `viper` selbst benennen und wieder löschen kann: eine **Regel** oder **Gruppe von Regeln** für den NATS-Port.
2. **Alle Maschinen einer Gruppe liegen in einem Netz, in dem sie sich über private Adressen erreichen** (eine Region, ein VPC, ein VNet). Ein Gruppenmitglied in einem anderen Netz oder einer anderen Region wird mit klarer Meldung abgelehnt, bevor etwas angelegt wird.
3. **Der NATS-Port wird nie für das Internet geöffnet** *[V, `nats.md` §4]*, und die Regel des Anbieters erlaubt ihn, wo sie nötig ist, nur für die Gruppe. **Ehrlich zur Grenze:** Die Schutzschichten sind dieselben wie bei Hetzner (`nats.md` §4): (1) der Server lauscht nur auf der privaten IP, (2) die Host-Firewall (`ufw`) erlaubt den Port nur aus dem Subnetz, (3) nur registrierte NKeys melden sich an, und die Clients vertrauen genau dem Zertifikat des Servers. Eine Regel des Anbieters ist **zusätzlich** und kann nicht enger sein, als die Standardregeln des Netzes zulassen: Azure-NSGs lassen mit `AllowVnetInBound` im ganzen VNet alles zu, die GCP-Standardnetz-Regel `default-allow-internal` ebenso. Dort schließt eine Erlauben-Regel nichts aus; „nur die Gruppe“ gilt dort für die Schichten (1) bis (3), nicht für das Netz. Das Handbuch sagt das (§4, Punkt 9).
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
* **Vor dem Anlegen:** Die Namen von Security Groups sind nur **je VPC** eindeutig. Zwei Maschinen derselben Gruppe mit verschiedenem `AWS_SUBNET_ID` in verschiedenen VPCs bekämen beide `created=true` und würden beide Server. Deshalb sucht `EnsurePrivateNetwork` vorher in der Region nach Security Groups mit dem Tag `viper-nats-group=<g>` und verweigert, wenn eine in einem anderen VPC liegt („alle Maschinen der Gruppe müssen im selben VPC sein“). Das ist keine Sperre (die ist das Anlegen), nur die Prüfung von Regel 2. Test dafür.
* **Anlegen/Sperre:** `EnsurePrivateNetwork` legt die Gruppe zuerst an (nicht `ensureGroup` aus `network.go` benutzen: das sucht erst und legt dann an) (mit `AWS_TAGS_SHARED` und `viper-managed`/Gruppe, damit Tag-Richtlinien nicht verweigern; das Handbuch beschreibt dieses Verhalten für Schlüsselpaar und Security Group *[V, README]*) und liefert `created=true`; bei `InvalidGroup.Duplicate` wird die vorhandene gefunden und geprüft (eigene Markierung, gleiches VPC).
* **Anbinden:** `AttachPrivateNetwork` liest die Security Groups der Instanz, ergänzt `viper-nats-<g>` und schreibt die ganze Liste zurück (`ModifyInstanceAttribute` **ersetzt** die Liste; die Gruppe für SSH muss erhalten bleiben; bei gleichzeitigem Ändern Wiederholung) und liefert `PrivateIpAddress`. Es ist idempotent: eine Instanz, die die Gruppe schon hat, wird nicht angefasst. Beim Anlegen kommt sie über `ServerSpec.Network` gleich mit (heute `Groups: []string{l.group}`; die zweite Gruppe wird ergänzt).
* **Metadaten:** Tags an der Instanz (`viper-managed`, `viper-nats-group`, `-role`, `-state`). `UpdateNATSMetadata` ändert Tags mit `CreateTags` (ergänzt/überschreibt nur diese vier) *[?]*; `ListGroupMachines` nutzt `DescribeInstances` mit Tag-Filter und überspringt beendete Instanzen.
* **Löschen:** `DeletePrivateNetwork` liest vorher die Markierung (nur eigene Gruppen werden gelöscht) und löscht die Security Group. `DependencyViolation` wird zu `ErrNetworkInUse`; **gleich nach dem Beenden der letzten Instanz** meldet AWS sie noch, solange die Netzwerkschnittstelle freigegeben wird: deshalb wird einige Sekunden wiederholt, bevor `ErrNetworkInUse` zurückkommt (sonst bleibt die Gruppe liegen; `down` warnt nur). Ob eine Regel, die auf die eigene Gruppe verweist, das Löschen blockiert, ist in M0 zu belegen *[?]*.
* **Rechte (IAM):** zusätzlich `ec2:CreateSecurityGroup`, `AuthorizeSecurityGroupIngress`, `ModifyInstanceAttribute`, `CreateTags`, `DeleteSecurityGroup`, `DescribeInstances`/`DescribeSecurityGroups` (teils schon nötig). Der Preflight meldet fehlende Rechte vor dem Anlegen *[?, wie prüft er das heute?]*.

### 3.3 Google Cloud

* **„Netzwerk der Gruppe“** = die VPC `GCP_NETWORK` plus eine Firewall-Regel `viper-nats-<g>` (Eingang, `tcp:<NATS_PORT>`, Quelle und Ziel: das Netzwerk-Tag `viper-nats-<g>`). `PrivateNetwork.ID` = Regelname, `Zone` = Region.
* **Sperre:** Anlegen der Regel; bei `alreadyExists` wird die Regel gelesen und geprüft (Markierung in der Beschreibung, gleiche Parameter). Firewall-Regeln tragen keine Labels; die Markierung steht in der **Beschreibung** *[?]*.
* **Anbinden:** das Netzwerk-Tag `viper-nats-<g>` an die Instanz: `setTags` **ersetzt alle Tags** und braucht den Fingerprint; die Instanz trägt schon das Tag der gemeinsamen Firewall (`gcp/sdk.go`), das ohne Zusammenführen verloren ginge und SSH sperrte. Also lesen, ergänzen, schreiben, bei `412` wiederholen; beim Anlegen über `ServerSpec.Network`. Private IP = `networkInterfaces[0].networkIP`.
* **Metadaten:** **Labels** der Instanz (`setLabels` mit `labelFingerprint`). Beschränkungen: Kleinbuchstaben, Ziffern, `-`, `_`, höchstens 63 Zeichen, höchstens 64 Labels; die vier `viper`-Labels passen *[?, Namen `viper-nats-group` etc. sind erlaubt]*. Gleichzeitige Änderungen verlangen eine Wiederholung bei `412` (Fingerprint).
* **Region:** `NetworkZone` bekommt bei GCP eine Zone (`app/provider.go` übergibt `ServerLocation`), die Regel selbst hat keine Region. Die Region steht in der Beschreibung der Regel (zusammen mit Markierung, Gruppe und Zeitstempel); beim `alreadyExists` wird außerdem das `network` der Regel mit `GCP_NETWORK` verglichen (Namen sind je Projekt eindeutig, die Regel einer anderen Gruppe oder eines anderen VPC darf nicht übernommen werden).
* **Löschen:** nach erneutem Lesen der Markierung die Regel löschen, wenn keine Instanz mit dem Tag mehr existiert (GCP kennt kein „in Gebrauch“; `viper` zählt die Mitglieder selbst und gibt sonst `ErrNetworkInUse`).
* **Rechte:** `compute.firewalls.create/delete/get`, `compute.instances.setTags/setLabels`.

### 3.4 Azure

**Zurückgestellt, bis M0 eine Sperre belegt hat** (Entscheidung §9). Gründe aus der Prüfung:

* Jede NIC hat genau die eine gemeinsame NSG `<prefix>-ssh` (`azure/sdk.go`); eine Gruppenregel müsste dort hinein (oder in eine NSG des Subnetzes, die nicht `viper` gehört), mit eindeutiger Priorität und einer Löschreihenfolge (eine ASG, die eine Regel nennt, lässt sich nicht löschen). Das widerspricht §8 („keine fremden Regeln ändern“) und braucht ein Prioritätenschema.
* NSGs enthalten die Standardregel `AllowVnetInBound` (Priorität 65000): Im VNet ist ohnehin alles erreichbar. Eine ASG-Regel schränkt nichts ein (§2, Regel 3). **Eine Regel ist für Azure daher gar nicht nötig;** es genügt, die private IP zu kennen und im Preflight/`check` zu prüfen, dass die Standardregel nicht überschrieben ist (der Verbindungstest nach dem Start zeigt es).
* Die Sperre: `CreateOrUpdate` ist idempotent und meldet keinen Konflikt. Blob-Leases brauchen ein Speicherkonto (neue Ressource, verletzt Regel 1), Verwaltungssperren sind keine gegenseitige Ausschließung, ein Tag mit Etag kann den Ersteller nicht erkennen. Zu prüfen in M0: ob der Statuscode der ARM-PUT-Antwort (201 beim Anlegen, 200 beim Aktualisieren, über die Antworterfassung des SDK lesbar) den Ersteller verrät, und ob `If-None-Match: *` für die gewählte Ressource gilt *[?]*.
* **Metadaten:** Tags der VM (`viper-managed`, Gruppe, Rolle, Zustand; bis zu 50 Tags); gleichzeitiges Ändern über eine `Merge`-Operation *[?]*.
* Private IP: die der NIC (bei „Deallocate“ nicht garantiert stabil *[?]*; `check` meldet die Abweichung).

Wenn M0 keine Sperre zeigt, bleibt Azure ohne NATS und das Handbuch sagt es.

## 4 Änderungen am vorhandenen Code

1. **Sperren öffnen:** die Prüfung in `settingsfile/config.go:152` ersetzen. NATS ist erlaubt für jeden Anbieter, dessen Adapter `NATSProvider` erfüllt; sonst die bisherige Meldung. Die Prüfung beim Laden kann den Adapter nicht fragen (Einstellungen kennen keine Adapter); sie prüft gegen eine Liste der Anbieter, die in der Anbietertabelle (`model/provider.go`) als `NATS: true` stehen *[?, wo passt das besser hin]*. Der Test `start/nats.go:48` bleibt als zweite Sicherung.
2. **Fachliche Begriffe klarstellen:** `PrivateNetwork` heißt bei AWS/GCP/Azure „Verbindungsregel der Gruppe“. Die Schnittstelle bleibt, die Kommentare in `port/driven/cloud/nats.go` sagen es (kein Umbenennen, damit der Hetzner-Teil unverändert bleibt).
3. **`NetworkZone`** wird je Anbieter belegt: AWS/GCP/Azure geben die Region zurück; die Prüfung „alle Server einer Gruppe in einer Zone“ bleibt in `start`.
4. **Anbinden und Mitgliedschaft:** `start` ruft `AttachPrivateNetwork` heute nur auf, wenn `srv.PrivateIP == ""`, und `check` leitet daraus ab, ob die Maschine im Netzwerk ist *[V, `start/nats.go`, `check/natschecks.go`]*. Bei Hetzner heißt leer „nicht angebunden“; bei AWS/GCP/Azure hat jeder Server eine private IP. Neu: `model.Server` bekommt ein Feld `InGroupNetwork` (der Server ist angebunden: bei Hetzner `PrivateIP != ""`, unverändert; bei den anderen: die Gruppen-Regel gilt für ihn, also Security Group, Tag oder ASG). `start` und `check` fragen dieses Feld; die Reihenfolge der Aufrufe bei Hetzner bleibt gleich. `PrivateIP` wird von den Adaptern gefüllt *[V, Feld vorhanden]*.
4a. **`PrivateNetwork.Created` und die Felder:** `natsRole` benutzt `Created` für das Fenster des ersten Starts, `start` und `check` zeigen `IPRange` und `Zone` an *[V]*. Ein Anbieterobjekt ohne eigenen Zeitstempel (AWS-Security-Group) bekommt einen in den Tags/der Beschreibung (`viper-created=<RFC 3339>`); `IPRange` ist der Adressbereich des Subnetzes, `Zone` die Region.
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
| 1 | Grundlage | `Server.InGroupNetwork` (§4, Punkt 4) in `start` und `check`, Hetzner unverändert (Test der Aufrufreihenfolge); Sperre in den Einstellungen öffnen (Anbietertabelle); Kommentare der Schnittstelle; Fake in `servertest` auf mehrere Anbieter ausrichten; Tests für „Anbieter ohne Fähigkeit“ bleiben |
| 2 | AWS | Adapter `NATSProvider` für AWS: Security Group als Verbindungsregel, Tags, private IP, Löschen; Unit-Tests gegen die nachgebildete API; **echter Lauf** mit zwei Instanzen (wie die vorhandenen `e2e`-Tests, Build-Tag `e2e`); Preflight-Rechte; Handbuch AWS |
| 3 | Google Cloud | wie AWS, ohne echten Lauf (siehe §7) |
| 4 | Azure | nur wenn M0 eine Sperre gefunden hat (§3.4); sonst entfällt der Meilenstein und das Handbuch nennt Azure als nicht unterstützt |
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

## 9 Entscheidungen

Entschieden am 11.11.2026 (ohne Rückfrage, nach der Prüfung in §11):

1. **Reihenfolge:** AWS, dann Google Cloud, dann Azure. AWS zuerst, weil nur dort ein echter Lauf möglich ist.
2. **Azure:** zurückgestellt, bis M0 eine Sperre belegt (§3.4).
3. **Google Cloud und Azure ohne echten Lauf:** Wenn ausgeliefert, dann mit der Aussage im Handbuch „gebaut und gegen nachgebildete Schnittstellen getestet, ein Lauf in einem echten Konto ist nicht belegt“ (wie die Anbieter selbst).
4. **Einstellung für zusätzliche Tags/Labels (Gegenstück zu `AWS_TAGS_SHARED`) für GCP/Azure:** keine. Es gibt kein Gegenstück bei den Anbietern, das einen Bedarf belegt; wer es braucht, meldet es.
5. **Anbietertabelle:** `NATS`-Flag in `model/provider.go`; die Prüfung beim Laden der Einstellungen richtet sich danach, der Test in `start/nats.go` bleibt als zweite Sicherung.

## 10 Definition of Done

1. Für jeden gelieferten Anbieter: `up --group` einer neuen NATS-Gruppe legt Server und Clients an, die Clients melden sich beim Server über private Adressen, `check` meldet alles in Ordnung, `down --group` löscht Maschinen und Verbindungsregel.
2. Zwei gleichzeitige `up` derselben neuen Gruppe ergeben genau einen Server (belegt je Anbieter: AWS real, die anderen gegen die nachgebildete API).
3. Der NATS-Port ist vom Internet aus nicht erreichbar und im privaten Netz nur für die Gruppe offen (geprüft von außen und durch die Regel).
4. Ein Anbieter ohne Fähigkeit meldet weiter klar, dass er sie nicht hat; Hetzner verhält sich unverändert.
5. Kein fremdes Netzwerk und keine Regel, die `viper` nicht angelegt hat, wird geändert oder gelöscht.
6. Das Handbuch nennt je Anbieter, was getestet ist und was nicht, und welche Rechte er braucht.

## 11 Prüfung (Stand 11.11.2026)

Der Plan wurde einmal von einem unabhängigen Prüfer gegen den Code gelesen; Anbieter-Verhalten konnte er nicht gegen Dokumentation oder ein Konto prüfen. Eingearbeitet:

| Befund | Änderung |
|---|---|
| `start` und `check` leiten „angebunden“ aus `PrivateIP == ""` ab; bei den neuen Anbietern ist sie immer gefüllt | `Server.InGroupNetwork` (§4, Punkt 4); M1 |
| AWS: Namen von Security Groups nur je VPC eindeutig | Prüfung über die Region vor dem Anlegen (§3.2) |
| Azure/GCP: Standardregeln lassen im Netz ohnehin alles zu; Erlauben-Regel schränkt nichts ein | Aussage ehrlich eingeschränkt (§2, Regel 3), Azure braucht keine Regel (§3.4) |
| Azure: gemeinsame NSG, Prioritäten, Löschreihenfolge, ungeeignete Sperren | Azure zurückgestellt (§3.4, §9) |
| `PrivateNetwork.Created`, `IPRange`, `Zone` werden benutzt | Zeitstempel und Felder festgelegt (§4, Punkt 4a) |
| `ModifyInstanceAttribute` und `setTags` ersetzen die ganze Liste | Lesen, Zusammenführen, Wiederholen (§3.2, §3.3) |
| `DeleteSecurityGroup` direkt nach dem Beenden scheitert zeitweise | Wiederholung vor `ErrNetworkInUse` (§3.2) |
| GCP: Zone statt Region; Regeln je Projekt; Besitz vor dem Löschen prüfen | Region in der Beschreibung, `network` vergleichen, Markierung erneut lesen (§3.3) |
| Tabelle in §1: Firewall ist je Präfix gemeinsam, nicht je Rechner | berichtigt |
| `ensureGroup` sucht erst und legt dann an | nicht benutzen (§3.2) |

Nicht geprüft: das Verhalten der Anbieter gegen Dokumentation (GCP-Label-Grenzen, Azure `Merge`, ARM `If-None-Match`, Löschen einer selbstverweisenden Security Group, Zeitverhalten der Netzwerkschnittstelle), die Rechteprüfung des Preflights und der Fake in `servertest`.

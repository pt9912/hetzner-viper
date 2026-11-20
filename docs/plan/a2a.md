# Plan: A2A-Brücke (`viper a2a`)

**Ziel:** Ein Agent, der **nicht** zur NATS-Gruppe gehört (ein anderes Framework, ein anderer Anbieter, ein Skript), soll einen Agenten auf einem Rechner der Gruppe über das offene **A2A-Protokoll** ansprechen können: Aufgabe stellen, Verlauf sehen, Rückfragen beantworten, Ergebnis und Dateien holen. `viper` übersetzt dabei A2A-Aufgaben in die NATS-Nachrichten, die es schon gibt (`request`, `reply`, Dateianhänge), und umgekehrt. Auf den Rechnern ändert sich nichts.

**Stand:** Entwurf (20.11.2026). Gebaut ist nichts. Die Entscheidungen in §9 sind **offen** und brauchen deine Antwort; zu jeder gibt es einen Vorschlag. Eine unabhängige Prüfung (20.11.2026) ist eingearbeitet (§10.1). *[V]* heißt: geprüft (A2A-Spezifikation 1.0.0 auf `a2a-protocol.org/latest/specification/`, Plan- und Code-Stand dieses Repositories); *[?]* ist eine Vermutung, die in Meilenstein 0 zu prüfen ist. **Die Spezifikation habe ich nicht Wort für Wort gelesen**, sondern mit gezielten Fragen zu Fassung, Bindungen, Operationen, Zuständen, Teilen, Agent-Card, Anmeldung, Strom, Push, Kennungen und Sicherheit ausgewertet; Einzelheiten (genaue Fehlercodes je Bindung, Feldlängen, Abschnitt 12 zu eigenen Bindungen) sind vor dem Bauen im Text nachzulesen.

Gehört zu [`nats.md`](nats.md) (die Gruppen), [`nats-mcp.md`](nats-mcp.md) (der Dienst auf dem Rechner), [`nats-cli.md`](nats-cli.md) (`viper nats`, der Operator) und [`ui-chat.md`](ui-chat.md) (dieselbe Verbindung vom eigenen Rechner aus: Tunnel, Strom, Verlauf).

## 1 Ausgangslage

* **Was es schon gibt** *[V]*: Die Rechner einer NATS-Gruppe reden über einen eigenen Dienst (`viper-nats-mcp`) miteinander. Nachrichten sind Umschläge `{"v":1,"kind":"msg|req|res","id","re","text","att"}` auf Subjects `viper.<g>.<absender>.out.<empfänger>` (Direktnachricht, Frage, Antwort) und `…pub.<thema>` (Rundruf). Der Absender steht im Subject und wird vom Server erzwungen. Der Operator (`viper nats init`) ist ein Eintrag mit eigenem Schlüssel; `viper nats` und der Chat sprechen über einen ssh-Tunnel zum Server. Eine Frage wartet höchstens 120 Sekunden; eine spätere Antwort kommt trotzdem im Strom an (`watch`, Zeile `res` mit `re`). `reply --to --re` beantwortet die Frage eines Rechners. Dateien (PDF, ZIP, Bilder) liegen im Objektspeicher der Gruppe (`NATS_OBJECTS`).
* **Was A2A ist** *[V]*: Ein Protokoll, mit dem ein **Client-Agent** einen **Server-Agenten** anspricht, ohne dessen Innenleben zu kennen. Der Server beschreibt sich in einer **Agent-Card** (`/.well-known/agent-card.json`: Name, Beschreibung, `supportedInterfaces`, `skills`, `capabilities`, `securitySchemes`). Der Client schickt eine **Nachricht** (`SendMessage`), bekommt eine **Aufgabe** (`Task`) mit Zustand (`SUBMITTED`, `WORKING`, `INPUT_REQUIRED`, `AUTH_REQUIRED`, `COMPLETED`, `FAILED`, `CANCELED`, `REJECTED`) und holt Ergebnisse als **Artefakte** ab, auf Wunsch als Strom (SSE). Drei Bindungen: JSON-RPC über HTTP, HTTP+JSON/REST und gRPC. Aktuelle Fassung 1.0.0; der Kopf `A2A-Version` wählt die Fassung, **ein leerer Wert gilt als 0.3**.
* **Warum eine Brücke und kein Umbau:** Claude Code spricht kein A2A *[V: Dokumentation, 20.11.2026]*. Die Rechner sollen nichts davon wissen: der Dienst `viper-nats-mcp` bleibt, wie er ist. Alles Neue liegt in einem Programmteil auf **dem Rechner des Operators** (wie der Chat), der A2A nach außen und NATS nach innen spricht.

## 2 Grundregeln

1. **Fremder Verkehr kommt nie unter der Identität des Operators in die Gruppe.** `NATS_MCP_PUSH_FROM=operator` bedeutet: Text des Operators landet ohne Rückfrage in der Sitzung eines Agenten. Käme ein A2A-Client darüber, erbte er dieses Vertrauen. Die Brücke bekommt darum einen **eigenen Namen** im Server (Standard `a2a`, §4.1); wer ihn nicht in `NATS_MCP_PUSH_FROM` führt, schiebt nichts in eine Sitzung.
2. **Nichts geht ohne Anmeldung nach außen.** Die Brücke lauscht standardmäßig nur auf `127.0.0.1`. Andere Adressen verlangen TLS und Zeichen (Token) je Aufrufer (§5).
3. **A2A erfindet keine Fähigkeit, die NATS nicht hat.** Es gibt keinen Fortschritt „zu 40 Prozent“, kein Abbrechen auf dem Rechner und keine Zustellbestätigung. Die Zustände der Aufgabe sagen nur, was die Brücke wirklich weiß (§3.2).
4. **Wiederverwenden:** Tunnel, Schlüssel, Anwesenheit, `request`, `reply`, Anhänge und Strom sind die Funktionen von `viper nats` und dem Chat (`openOperator`, `natsmcp.RunCLI`, `chatstream`). Die Brücke ist Übersetzung und Verwaltung der Aufgaben.
5. **Alles, was von außen kommt, ist Text eines Fremden**, und alles, was hinausgeht, kann ein Geheimnis enthalten: Bereinigung und Schwärzen wie im Chat (`natsmcp.Clean`, `[hidden]`), in beide Richtungen.
6. **Ein Rechner ist ein Agent** (Agent-Card, Pfad, Berechtigung). Die Gruppe ist der Rahmen, nicht der Agent.
7. **Jede Anfrage ist angemeldet** (außer der Karte, wenn der Betreiber sie ausdrücklich öffentlich macht, §5.2). *[V: der Server MUST jede Anfrage anmelden]*

## 3 Abbildung A2A ⇄ NATS

### 3.1 Wer ist wer

| A2A | NATS / viper |
|---|---|
| Server-Agent (eigene Agent-Card) | **ein Rechner** der Gruppe (`odata-recorder-w3`) |
| Mandant (`tenant`, Pfad) | `<gruppe>/<rechner>` |
| Client-Agent, Aufrufer (`principal`) | ein Token aus der Datei der Brücke (§5); nach NATS tritt **die Brücke** als `a2a` auf |
| `Message` (Rolle `ROLE_USER`) | Umschlag `req` von `a2a` an den Rechner |
| Antwort des Rechners | Umschlag `res` mit `re` = ID der Frage |
| `Task` | Eintrag der Brücke (§3.2), verknüpft mit der ID der Frage |
| `contextId` | ein Gespräch eines Aufrufers mit **einem** Rechner (von der Brücke vergeben) |
| `Artifact` | die Antwort des Rechners (Text und angehängte Dateien) |

### 3.2 Zustände einer Aufgabe

NATS kennt keinen Fortschritt. Die Brücke sagt darum nur, was sie weiß:

| Zustand | Wann |
|---|---|
| `SUBMITTED` | Die Brücke hat die Aufgabe angenommen und die Frage an den Server **gegeben** (nicht: zugestellt) |
| `WORKING` | Die Frage ist raus **und** der Rechner hat sich in den letzten 90 Sekunden als verbunden gemeldet (Anwesenheit). Das heißt „verbunden“, **nicht** „arbeitet“: eine Anwesenheitsmeldung sagt nichts über die Sitzung des Agenten. Ein Rechner ohne Anwesenheit bleibt `SUBMITTED` |
| `INPUT_REQUIRED` | Der Rechner, **an den die Aufgabe ging**, hat der Brücke eine **Frage** gestellt (Umschlag `req` an `a2a`); der Text steht in `status.message`. Die Antwort des Aufrufers (neue Nachricht mit derselben `taskId`) geht als `reply --re` zurück. **Grenzen: §3.2.1** |
| `COMPLETED` | Der Strom liefert `res` mit `re` = ID der Frage **vom Angefragten** an die Brücke; der Text und die Dateien werden zum Artefakt. **Die erste solche `res` ist die endgültige;** spätere mit demselben `re` werden verworfen (und im Verlauf als „weitere Antwort“ vermerkt), denn ein Agent kann nicht sagen, dass noch weitere folgen |
| `FAILED` | Die Frist der Aufgabe ist um (Standard 1 Stunde, §3.4), der Tunnel oder Server ist nicht erreichbar, oder die Frage wurde vom Server abgelehnt (Berechtigung, Größe). `status.message` nennt den Grund |
| `CANCELED` | Der Aufrufer hat `CancelTask` gerufen, bevor ein Ende kam. Auf dem Rechner wird nichts abgebrochen; eine spätere Antwort wird verworfen (der Rechner kann nicht gefragt werden, ob er sie noch braucht) |
| `REJECTED` | Nur für eine Aufgabe, die **angelegt** wurde und deren Ausführung die Brücke dann ablehnt (zum Beispiel: der Rechner meldet sich nach der Annahme mit `bye` ab). Fehler **vor** einer Aufgabe (zu groß, unbekannter Inhaltstyp, kein Zugriff, Grenze überschritten) sind **Fehler der Anfrage** (`InvalidParams`, `ContentTypeNotSupportedError`, HTTP 404/429) und legen keine Aufgabe an |
| `AUTH_REQUIRED` | wird nicht gesetzt (§7) |

Beendet (terminal) sind `COMPLETED`, `FAILED`, `CANCELED` und `REJECTED`; unterbrochen sind `INPUT_REQUIRED` und `AUTH_REQUIRED` *[V]*. Eine beendete Aufgabe nimmt keine Nachricht mehr an (`UnsupportedOperationError`); eine neue Nachricht im selben `contextId` ohne `taskId` ist eine **neue** Aufgabe *[V]*. `taskId` vergibt immer die Brücke *[V]*.

#### 3.2.1 Fragen der Rechner (`INPUT_REQUIRED`) und ihre Grenzen

Ein Umschlag trägt keinen Bezug zu einer Aufgabe (`v, kind, id, re, text, att`). Darum:

* Eine Frage an `a2a` wird **nur** einer Aufgabe zugeordnet, wenn für diesen Rechner **genau eine** Aufgabe offen ist. Sind es mehrere, **ändert die Frage keinen Zustand**: sie kommt in den Verlauf aller offenen Aufgaben dieses Rechners als „nicht zugeordnete Frage“ und wird gezählt. Eine Frage eines Rechners, für den keine Aufgabe offen ist, wird verworfen. Jeder Rechner darf `a2a` anschreiben, und der Eintrag `a2a` liest die ganze Gruppe: ein Rechner, der nicht gefragt wurde, darf keine Aufgabe eines Aufrufers beeinflussen.
* Die Kennzeile (§5.4) nennt die Aufgabe; ein Rechner kann `task <id>` in seine Frage schreiben. Das ist eine **Verabredung**, keine Prüfung.
* Der Rechner wartet auf seine Frage **höchstens 120 Sekunden** (`request`). Antwortet der Aufrufer später, kommt die `res` beim Rechner ohne wartende Frage an und wird dort **nur protokolliert** („answer … to no open request“). Die Antwort geht dann verloren. Die Brücke sagt das in `status.message` („Der Rechner wartet höchstens zwei Minuten auf die Antwort“) und setzt die Aufgabe nach Ablauf dieser Frist wieder auf `WORKING`.
* Meilenstein 3 prüft das mit einem Rechner als Dienst, bevor mehr darauf gebaut wird.

### 3.3 Operationen (Fassung 1.0.0 *[V]*)

| Operation | Umsetzung |
|---|---|
| Agent-Card | Je Rechner eine Karte unter `/<gruppe>/<rechner>/.well-known/agent-card.json` und eine Liste der Rechner unter `/<gruppe>/`, die **angemeldet** nur die Rechner nennt, die der Aufrufer ansprechen darf. `supportedInterfaces`: JSON-RPC (und später HTTP+JSON) mit `protocolVersion` `1.0`, `tenant` = `<gruppe>/<rechner>` (der Pfad ist die Adresse, `tenant` der Wert, den der Client laut Karte mitsenden muss; die Brücke prüft, dass beide zusammenpassen). `defaultInputModes` nennt nur, was angenommen wird: `text/plain` und die Arten aus §3.3 (PDF, ZIP, PNG, JPEG, GIF, WebP). Pflichtfelder (`name`, `description`, `supportedInterfaces`, `version`, `capabilities`, `defaultInputModes`, `defaultOutputModes`, `skills`) kommen aus den Einstellungen des Rechners (§4.3) und aus der Fassung von `viper` |
| `SendMessage` | Nachricht prüfen → Aufgabe anlegen (`SUBMITTED`) → die Frage an den Rechner (der Text der Teile `text` mit der Kennzeile von §5.4). Ohne `returnImmediately` (Standard: **blockierend**) hält die Brücke den HTTP-Aufruf offen, bis die Aufgabe **beendet oder unterbrochen** ist *[V: „MUST wait until the task reaches a terminal or interrupted state“]*, höchstens bis zur Frist der Aufgabe (Standard 1 Stunde, mit Schreibfrist des Servers). Die 120 Sekunden von `request` braucht die Brücke dafür **nicht**: sie ordnet die Antwort über den Strom zu (§3.4). Mit `returnImmediately: true` kommt die Aufgabe sofort zurück. Clients mit kurzen Fristen nehmen `SendStreamingMessage` oder `returnImmediately` (§9.9) |
| `SendStreamingMessage`, `SubscribeToTask` | SSE; Ereignisse nur an den Übergängen der Zustände (§3.2) und ein `artifactUpdate` mit der Antwort. Keine Teilergebnisse (NATS liefert die Antwort als **eine** Nachricht). Bei JSON-RPC ist jedes Ereignis eine JSON-RPC-Antwort mit `result` = `StreamResponse`, bei REST das `StreamResponse` selbst *[V]*. `capabilities.streaming` ist **`false` bis Meilenstein 3**, dann `true` |
| `GetTask`, `ListTasks` | aus dem Speicher der Brücke (§3.4); `historyLength` und Seiten (50 bis 100, Cursor, absteigend nach Zeit) wie in der Spezifikation; nur Aufgaben des **eigenen Aufrufers** |
| `CancelTask` | `CANCELED`, wenn nicht beendet; sonst `TaskNotCancelableError`. Eine Aufgabe, die es nicht gibt oder die einem anderen Aufrufer gehört: `TaskNotFoundError` |
| Push-Benachrichtigung (Webhook) | **nicht in Fassung 1** (§7): `capabilities.pushNotifications = false`, die Operationen antworten `PushNotificationNotSupportedError` |
| `GetExtendedAgentCard` | nicht gebaut: `capabilities.extendedAgentCard = false`, Antwort `ExtendedAgentCardNotConfiguredError` |

**Teile (`Part`) nach innen:** `text` (Pflicht, sonst `REJECTED`), `raw` (Bytes, Base64) für PDF, ZIP und Bilder bis zur Grenze der Gruppe (`NATS_OBJECTS_FILE_MAX`, höchstens 64 MiB), Art nach den ersten Bytes wie auf den Rechnern (`natsmcp.Classify`, `CheckAttachment`); **andere Arten (`text/plain`, JSON, Office) werden abgelehnt**, auch wenn Clients sie oft schicken. Eine Gruppe ohne `NATS_OBJECTS` nimmt keine Dateien (`ContentTypeNotSupportedError`). **Speicher:** 64 MiB Base64 sind rund 86 MiB JSON; die Brücke begrenzt den Körper **vor** dem Lesen (`MaxBytesReader`), liest Dateien nicht doppelt in den Speicher und begrenzt die Summe aller laufenden Anfragen (Standard 256 MiB). Der Text einschließlich der Kennzeile darf **16 KiB** nicht überschreiten; `data` (strukturiertes JSON) wird nicht unterstützt (`ContentTypeNotSupportedError`); **`url` wird abgelehnt** (die Brücke lädt nichts von Adressen, die ein Fremder nennt: SSRF, §5). Eine Datei je Nachricht (wie im Chat).

**Teile nach außen:** der Text der Antwort als `text`; eine Datei der Antwort (`att_*`) holt die Brücke mit `fetch` und liefert sie als `raw` bis zu einer kleinen Grenze (Vorschlag 4 MiB), größere als `url` auf einen **Pfad der Brücke** (`/<gruppe>/<rechner>/files/<id>`), der Token **und Aufgabe** prüft (nur der Aufrufer, dessen Aufgabe die Datei nennt; die Objekt-ID sehen alle Rechner der Gruppe) und die Datei immer als Download ausliefert (`Content-Disposition: attachment`, `nosniff`, wie der Chat). Die geholte Datei wird mit der Aufgabe gelöscht.

### 3.4 Speicher der Aufgaben

* Ein Eintrag je Aufgabe: `taskId`, `contextId`, Aufrufer, Rechner, ID der Frage (`re`), Zustand, Zeiten, Verlauf (die Nachrichten beider Richtungen), Artefakte. Datei je Aufgabe unter `<Zustandsverzeichnis>/a2a/<gruppe>/` (`0600`/`0700`), mit Alter (Standard 7 Tage) und Größe (Standard 256 MiB je Gruppe) begrenzt; `--no-history` hält sie nur im Speicher. Wie beim Chat: bekannte Zugangsdaten werden **vor dem Schreiben** geschwärzt.
* **Frist einer Aufgabe** (Standard 1 Stunde, einstellbar): danach `FAILED` „keine Antwort“. Das ist länger als die 120 Sekunden von `request`: die Antwort wird über den **Strom** zugeordnet (`re`), nicht über das Warten des Befehls. Das ist dieselbe Technik wie die späte Antwort im Chat.
* Neustart der Brücke: offene Aufgaben werden aus den Dateien geladen; eine Frage, deren Antwort in der Lücke kam, bleibt offen, bis die Frist ausläuft (der Strom holt nichts nach).

## 4 Architektur

```
 A2A-Client ──HTTPS/Token──▶ viper a2a serve (Operator-Rechner) ──ssh-Tunnel──▶ NATS-Server ◀──▶ Rechner (viper-nats-mcp)
                              │  Agent-Cards, JSON-RPC, SSE         │                              Sender a2a / Operator getrennt
                              │  Aufgaben, Speicher, Schwärzen      └─ Strom `watch` (Zeilen an a2a), `request --announce`, `reply`, `fetch`
```

### 4.1 Eigene Identität der Brücke

* Neu: `viper a2a init GRUPPE` trägt einen **zweiten Operator-Eintrag** mit dem Namen `a2a` (Einstellung `NATS_A2A_NAME`, Standard `a2a`) im Server ein; Schlüssel in `groups/<gruppe>/a2a.nk` (wie `operator.nk`, `0600`). Die Rechte sind die des Operators (`OperatorPermissions`): senden nur im eigenen Teilbaum `viper.<g>.a2a.>`, lesen die Gruppe. `viper a2a revoke GRUPPE` entfernt ihn. *[V, im Code: Server und `Register`/`Revoke` tragen mehrere Operator-Einträge je Gruppe; `checkedClients` verbietet nur gleiche Namen, `ParseClients`, `WithEntry` und `WithoutOperator` arbeiten mit dem Namen. `natsserver` und `natsop` brauchen **nur einen Test**. Zu ändern ist `cli`: `natsGroup.operator` ist **ein** Name; dazu `NATS_OPERATOR_NAME` und die Prüfung in `settingsfile` (der Name `a2a` ist für Rechner **und** für den Operator gesperrt) und die neuen `NATS_A2A_*` in `settingsfile`, `model.NATSConfig`, den Einstellungen der Oberfläche und in `viper check`.]*
* Die **Identitäten mit Namen** (`viper nats init --name`, Schlüsseldatei je Name) baut zuerst der Plan [`nats-lokal.md`](nats-lokal.md) §3; `viper a2a init` nutzt sie.
* Der Name `a2a` ist für Rechner **gesperrt** (wie der Name des Operators, `settingsfile.Load`).

### 4.2 Schichten (nach dem Muster des Chats)

| Schicht | Inhalt |
|---|---|
| `internal/usecase/a2a` | Aufgaben, Zustände, Zuordnung von Zeilen des Stroms zu Aufgaben (`re`, Frist), Fragen der Rechner (§3.2.1), Rechte je Aufrufer, Grenzen. Kein Netz: NATS kommt über den Port `a2anats` |
| `internal/port/driving/a2a.go` | `A2A`-Schnittstelle (SendMessage, GetTask, ListTasks, CancelTask, Subscribe) und Typen |
| `internal/port/driven/a2astore` | Speicher der Aufgaben (Datei und Speicher als Adapter) |
| `internal/port/driven/a2anats` | der Weg nach NATS für den Anwendungsfall: Frage senden, Antwort senden, Datei ablegen und holen, Strom der Zeilen an `a2a`, Anwesenheit. Der Adapter `internal/adapter/driven/a2anats` hält **eine** dauerhafte Verbindung (`natsmcp.Service`) und wird in `app` verdrahtet. **Nicht** `RunCLI` je Aufgabe: das verbindet jedes Mal neu und wartet 200 ms (`settle`) |
| `internal/adapter/driving/a2aweb` | HTTP-Server: Agent-Cards, JSON-RPC, SSE, später REST; Anmeldung, TLS, Grenzen; **kein** Wissen über NATS |
| `internal/adapter/driving/cli` | `viper a2a …`, Tunnel (`openOperatorDead`, mit dem Namen `a2a`), Verdrahtung. Ein Treiber-Adapter darf **keinen** Anwendungsfall und keinen Treiber der anderen Seite einbinden (depguard, `.golangci.yml`): NATS erreicht den Anwendungsfall nur über `a2anats` |
| `internal/natsmcp` | **nicht unverändert:** eine kleine **ausgeführte** Schnittstelle (senden, antworten, Datei, Strom, Anwesenheit), denn `peers.Present` und der Mitleser sind heute nicht ausgeführt. Ein `watch --to` ist **nicht** nötig: ohne `--all` kommen nur Zeilen an den eigenen Namen (`viper.<g>.*.out.<Me>`) |

### 4.3 Einstellungen

Je Rechner (in den Einstellungen des Rechners, nur Beschreibung, keine Berechtigung): `NATS_A2A_ENABLED=true` (der Rechner wird als Agent angeboten; Standard **aus**), `NATS_A2A_DESCRIPTION` (Text der Karte), `NATS_A2A_SKILLS` (Liste `id:Name:Beschreibung`; ohne Angabe eine Fähigkeit `ask`: „Beantwortet eine Frage oder erledigt eine Aufgabe des Agenten auf diesem Rechner“). Je Gruppe: `NATS_A2A_NAME`. Die Berechtigungen der Aufrufer stehen **nicht** in den Einstellungen der Rechner (§5.2).

## 5 Sicherheitsmodell

### 5.1 Was die Brücke an Gefahren neu schafft

Ein Fremder, der die Brücke erreicht, kann Text in den Agenten eines Rechners bringen. Das ist die **Prompt-Injektion von außen** und dieselbe Gefahr wie bei `NATS_MCP_PUSH_FROM`, nur mit einem Netzwerk dazwischen. Darum: kein Zugriff ohne Anmeldung, Zustimmung des Betreibers je Rechner (`NATS_A2A_ENABLED`), eine Identität, die nicht der Operator ist, und die Kennzeile (§5.4).

### 5.2 Anmeldung und Berechtigung

* **Anmeldung:** Karte und Aufrufe verlangen ein **Bearer-Token** (`securitySchemes`: `HTTPAuthSecurityScheme`, Schema `bearer`). Die Tokens stehen in einer Datei der Brücke (`<Zustandsverzeichnis>/a2a/tokens`, `0600`): je Zeile Name des Aufrufers, **Hash** des Tokens, erlaubte Rechner (`gruppe/rechner` oder `*`), Grenzen (Aufgaben, Aufrufe je Minute). `viper a2a token add NAME --machines …` gibt das Token **einmal** aus. Die Agent-Card selbst ist öffentlich lesbar, wenn `--public-card` gesetzt ist; **jeder andere Pfad verlangt das Token immer**, auch die Liste der Rechner. Das Token wird als **Hash** verglichen, mit `subtle.ConstantTimeCompare`; Namen der Aufrufer sind `CheckNATSName`-gültig (§5.4). Weitere Verfahren (OAuth, OpenID, mTLS): nicht in Fassung 1 (§7).
* **Berechtigung:** je Aufrufer die Liste der Rechner. Eine Anfrage an einen anderen Rechner und eine an einen Rechner, den es nicht gibt, bekommen **dieselbe** Antwort (404, kein Hinweis, ob es ihn gibt) und legen **keine** Aufgabe an.
* **Isolation:** `GetTask`, `ListTasks`, `CancelTask` und `SubscribeToTask` zeigen nur Aufgaben des **eigenen** Aufrufers; die Prüfung kommt **vor** jedem Zugriff auf den Speicher *[V: Spezifikation §13.1, MUST]*.
* **Grenzen je Aufrufer:** offene Aufgaben (Standard 4), Nachrichten je Minute (Standard 10), Größe (Text 16 KiB, Datei wie in §3.3). Überschritten: HTTP 429, keine Aufgabe. **Weitere Grenzen:** offene SSE-Ströme je Aufrufer (Standard 4) und insgesamt (32), Fristen für das Lesen des Kopfes und das Schreiben, Aufgaben im Speicher je Aufrufer, Körper vor dem Lesen begrenzt. Die Anfrageverarbeitung macht `net/http`; nichts davon wird von Hand zerlegt.

### 5.3 Nach außen erreichbar machen

* Standard `--listen 127.0.0.1:PORT`. Eine andere Adresse verlangt `--tls-cert` und `--tls-key` **oder** ausdrücklich `--behind-proxy` (TLS endet davor; die Brücke vertraut dann `X-Forwarded-*` **nicht**). Folge: Alle Aufrufer haben die Adresse des Proxys, darum begrenzt die Brücke **je Token-Name**, und Fehler der Anmeldung zählt sie **insgesamt**, nicht je Adresse. Ohne Tokendatei startet die Brücke bei einer fremden Adresse nicht.
* Die Karte nennt die **öffentliche Adresse** (`--public-url`), nicht die, an die gebunden ist.
* **Lokaler Port:** Die Brücke antwortet nur unter dem Namen, unter dem sie gerufen wird (die Prüfung des Kopfes `Host` von `viper ui`, gegen DNS-Rebinding), und verweigert Anfragen von Seiten im Browser (kein CORS, Prüfung von `Origin`). Andere Benutzer desselben Rechners erreichen `127.0.0.1`; dort schützt allein das Token. Das Handbuch sagt es.
* Das Handbuch rät zum Weg über einen vorhandenen Zugang (ssh-Tunnel, VPN, Reverse-Proxy mit TLS) statt eines offenen Ports.

### 5.4 Was an den Agenten geht

* Der Text beginnt mit **einer festen Zeile**: `[A2A: <Aufrufer>, Aufgabe <id>]`, dann eine Leerzeile, dann der Text des Aufrufers. So sieht der Agent, dass es Text von außen ist (und von wem). Der Name des Aufrufers ist `CheckNATSName`-gültig (kein Zeilenende, keine Klammer). Zeilen im Text des Aufrufers, die mit `[A2A:` beginnen, werden entschärft.
* **Das ist ein Hinweis, kein Schutz.** Ein Aufrufer kann nach der Leerzeile alles schreiben, auch ein falsches „Ende der Nachricht“ mit Anweisungen. **Prompt-Injektion verhindert das nicht.** Die eigentlichen Kontrollen sind: `a2a` nicht in `NATS_MCP_PUSH_FROM`, das Opt-in je Rechner und das Token mit seiner Rechnerliste. Antworten gehen ohne Zeile hinaus und tragen in `metadata` den Vermerk, dass sie von einem Rechner kommen und nicht geprüft sind.
* Der Rechner sieht den Absender `a2a` (aus dem Subject, vom Server erzwungen). Nur wenn der Betreiber `a2a` in `NATS_MCP_PUSH_FROM` des Rechners führt, erscheint der Text in der Sitzung; sonst nur im Posteingang. **`viper a2a serve` fragt beim Start** (wie der Chat bei `NATS_MCP_PUSH_FROM`), wenn ein Rechner mit `NATS_A2A_ENABLED` den Namen `a2a` in der Liste führt, und verlangt `--yes`.

### 5.5 Was nicht geschützt ist

1. **Nichts ist privat in der Gruppe** (`nats-mcp.md` §3): jede Maschine liest die Frage des Aufrufers und die Antwort, auch Rechner, die A2A nicht anbieten. Das Handbuch sagt es; kein Vertrauliches über A2A, was die Rechner einander nicht lesen dürfen.
2. **Antworten gehen nach außen.** Die Brücke schwärzt bekannte Zugangsdaten (`[hidden]`, wie `viper ui`); ein Geheimnis ohne bekannten Namen kann herauskommen. Das ist die Wahl des Betreibers mit `NATS_A2A_ENABLED`.
3. **Ein übernommener Rechner** kann in seiner Antwort jeden Text an den Aufrufer schicken (Injektion in **dessen** Agenten). Die Brücke kann das nicht verhindern; sie liefert die Antwort als Text und nennt in `metadata` den Rechner.
4. **Wiederholung:** Ein Token, das gestohlen wird, ist bis zum Widerruf gültig (`viper a2a token revoke`).
5. **SSRF:** Es gibt keine Webhooks und keine Teile mit `url`; die Brücke ruft keine Adresse auf, die ein Aufrufer nennt *[V: die Spezifikation empfiehlt dort Prüfungen (SHOULD); wir lassen die Funktion weg]*.

### 5.6 Protokoll und Spuren

* `events.log` bekommt eine Zeile je Aufgabe (Zeit, Aufrufer, Rechner, Zustand am Ende), **ohne** Inhalt, ohne Token und ohne Kennung der Aufgabe. Anmeldefehler werden **insgesamt** gezählt und begrenzt (nach 10 Fehlern eine Minute Pause für alle Fehlversuche; das Token eines Aufrufers wird dadurch nicht gesperrt, damit kein Fremder es aussperren kann).
* Ein Lauf von `viper a2a serve` ist ein Vordergrundprogramm (Strg-C beendet es); kein Dienst, keine Autostart.

## 6 Umsetzung

### 6.1 Befehle

```
viper a2a init GRUPPE            # Identität a2a im Server anlegen (wie viper nats init)
viper a2a revoke GRUPPE
viper a2a token add NAME [--machines g/r,…] [--rate N] [--tasks N]   # gibt das Token einmal aus
viper a2a token list|revoke NAME
viper a2a serve [GRUPPE…] [--listen ADR] [--public-url URL] [--tls-cert F --tls-key F | --behind-proxy]
                [--public-card] [--keep-days N] [--no-history] [--yes] [--readonly]
viper a2a check [GRUPPE]         # Karten, Anwesenheit, Rechte, ein Probeaufruf an sich selbst
```

`--readonly`: die Brücke beantwortet nur Anfragen nach Karten und Aufgaben (`GetTask`, `ListTasks`), nimmt aber keine Nachricht an.

### 6.2 Protokolltreue

* **Fassung 1.0** (`protocolVersion: "1.0"`). Der Kopf `A2A-Version` wird ausgewertet: `1.0` gilt; **fehlt er, gilt nach der Spezifikation 0.3**, und die Brücke antwortet `VersionNotSupportedError` mit einem Hinweis. Eine Verträglichkeit mit 0.3 (andere Methodennamen und Felder) ist Meilenstein 7. Der Fehler `VersionNotSupportedError` hat den JSON-RPC-Code `-32009` *[V]*.
* **Bindung:** JSON-RPC über HTTP (`POST /<gruppe>/<rechner>/rpc`, PascalCase-Methoden; die Spezifikation schreibt den Pfad nicht vor) und SSE für Ströme. *[?: ob eine einzige Bindung genügt, steht nicht ausdrücklich im gelesenen Text; wir lesen es so, dass die Karte nennt, was geboten wird.]* HTTP+JSON/REST ist Meilenstein 6, gRPC ist nicht geplant (§7).
* **Bibliothek:** Eine eigene schlanke Umsetzung (Typen, JSON-RPC, SSE, rund 800 Zeilen) oder das offizielle Go-SDK von A2A; in Meilenstein 0 gemessen an Größe (`go.mod`, `go.sum`), Fassung 1.0, Lizenz und Anschluss an die Schichten. Vorschlag: eigene Umsetzung, wie bei MCP (`nats-mcp.md` §2), außer das SDK ist klein und hat nur die Typen.

### 6.3 Änderungen am vorhandenen Code

| Stelle | Änderung |
|---|---|
| `internal/natsserver`, `internal/usecase/natsop` | nur ein Test für zwei Operator-Einträge je Gruppe (Name `a2a`) |
| `internal/adapter/driving/cli` | Befehle `a2a …`, Hilfe, Befehlstabelle; `loadNatsGroup` kennt die Identität `a2a` (Schlüsseldatei, Name); `openOperatorDead` mit Name statt festem Operator |
| `internal/natsmcp` | eine kleine ausgeführte Schnittstelle für die dauerhafte Verbindung (senden, antworten, Datei, Strom, Anwesenheit; §4.2). Kein `watch --to` |
| `internal/model`, `settingsfile`, Einstellungen der Oberfläche, `viper check` | Name `a2a` gesperrt (Rechner und Operator); `NATS_A2A_*` je Rechner und Gruppe; Typen der Aufgaben |
| Neue Pakete | `usecase/a2a`, `port/driving/a2a.go`, `port/driven/a2astore`, `port/driven/a2anats`, `adapter/driven/a2astore`, `adapter/driven/a2anats`, `adapter/driving/a2aweb` (importiert **nur** `port/driving`) |
| Handbuch, README, Hilfe | Kapitel „Rechner über A2A erreichbar machen“ (Ist-Zustand), Befehlstabelle |

## 7 Bewusst nicht enthalten (Fassung 1)

* **gRPC-Bindung** (braucht Protobuf und viel Abhängigkeit; *[?]* die Spezifikation verlangt, so weit gelesen, nicht alle Bindungen).
* **Push-Benachrichtigungen (Webhooks):** SSRF-Prüfung, Speicher für Geheimnisse, Wiederholungen. Aufrufer nehmen `GetTask` oder `SubscribeToTask`.
* **`AUTH_REQUIRED`, `GetExtendedAgentCard`, OAuth/OpenID/mTLS, signierte Karten.**
* **Teile `url`** und `data` in Nachrichten nach innen.
* **A2A in die andere Richtung** (ein Agent der Gruppe ruft einen fremden A2A-Agenten): Dafür braucht jeder Gesprächspartner eine eigene Identität im Server, oder der Dienst auf dem Rechner bekommt ein Werkzeug `a2a_call` (ohne NATS, aus dem Rechner ins Netz). Beides ist ein eigener Plan.
* **Fortschritt, Teilergebnisse, Abbrechen auf dem Rechner:** gibt NATS nicht her.
* **JetStream und garantierte Zustellung** (`nats-jetstream-nuntius.md`).
* **Mehrere Brücken für dieselbe Gruppe** (zwei Läufe mit demselben Schlüssel würden Antworten doppelt sehen; eine Sperrdatei wie beim Chat verhindert das).

## 8 Meilensteine

| | Inhalt | Prüfung |
|---|---|---|
| 0 | **Verifikation:** Test mit zwei Operator-Einträgen im Server; ein Rechner schickt `req` an `a2a`, und der Strom zeigt sie; ein **offener** HTTP-Aufruf über eine Stunde (Fristen von `net/http` und eines Proxys); eine Antwort, die nach 120 Sekunden kommt, und die Frage eines Rechners nach der Frist (§3.2.1); Fassung 1.0 gegen einen **fremden Client** (ein A2A-SDK oder der Prüfer der Gemeinschaft) mit einer Handvoll Karten und einer Nachricht; Größe und Fassung des Go-SDK; die Teile der Spezifikation, die nur über Fragen ausgewertet sind (Fehlercodes je Bindung, Abschnitt 12) | Messungen im Plan, Entscheidung zu Bibliothek, Version 0.3 und zum offenen Aufruf |
| 1 | **Gerüst:** `viper a2a init|revoke|serve|check` und `token add|list|revoke`, Identität `a2a` (cli, `settingsfile`, `model`, `check`), Agent-Cards je Rechner (ohne Strom), Anmeldung mit Token (Hash, konstante Zeit), `Host`-Prüfung, `127.0.0.1`, Sperrdatei, die Frage nach `PUSH_FROM` beim Start (`--yes`), `--readonly` | Go-Tests (Karte, Anmeldung, Name gesperrt, zwei Operator-Einträge), HTTP-Tests mit dem echten Programm (ohne Browser) |
| 2 | **Aufgaben:** `SendMessage` blockierend (offener Aufruf bis zum Ende), die Frage über die dauerhafte Verbindung (`a2anats`), Speicher, `GetTask`, `ListTasks`, `CancelTask`, Zustände, Fristen, Isolation | Tests mit eingebettetem NATS-Server (Rechner als Dienst), Tabellentests der Zustände, Isolation zweier Aufrufer; **ein Lauf gegen einen fremden Client** (Karte, Nachricht, Aufgabe) |
| 3 | **Strom und Rückfragen:** `SendStreamingMessage`, `SubscribeToTask` (SSE), späte Antworten über `re`, `INPUT_REQUIRED` mit `reply` | Tests mit Verzögerungen und mit zwei offenen Aufgaben für einen Rechner (§3.2.1), Neustart der Brücke mit offener Aufgabe; ein Lauf gegen einen fremden Client mit Strom |
| 4 | **Dateien:** `raw` nach innen (Prüfung wie die Rechner), `raw`/`url` nach außen mit `fetch`, Download-Pfad | Tests der Grenzen, Kopfzeilen des Downloads |
| 5 | **Härten:** TLS, `--behind-proxy`, Grenzen je Aufrufer, 429, Schwärzen, Kennzeile, `events.log`, Brute-Force-Bremse (die Tokenbefehle sind schon in Meilenstein 1) | Tests der Angriffe (§5: falsches und gestohlenes Token, fremde Aufgabe, falscher Rechner, Kennzeile mit Zeilenumbruch, riesige Körper, viele Ströme, `Host` und `Origin`), Fuzz von JSON-RPC, unabhängige Prüfung; ein letzter Lauf gegen einen fremden Client |
| 6 | **HTTP+JSON/REST** (`/message:send` usw.) | dieselben Fälle über REST |
| 7 | **Handbuch, README, Hilfe** (Ist-Zustand, keine Chronik); optional **Verträglichkeit mit 0.3** | Durchsicht, unabhängige Prüfung |

Jeder Meilenstein endet mit grüner CI auf Linux, macOS und Windows (Go-Tests, Lint und die HTTP-Tests) und einer unabhängigen Prüfung, wie bei `viper ui` und beim Chat. Die Brücke braucht **keine** neuen Rechte auf den Rechnern, nur den zusätzlichen Eintrag im Server.

## 9 Entscheidungen (offen, mit Vorschlägen)

Jede Frage hat die Möglichkeiten, einen **Vorschlag** mit Begründung, die Folge und den Zeitpunkt: **jetzt** (bestimmt die Bauart und ist später teuer zu ändern) oder **nach Meilenstein 0** (hängt an einer Messung). Wenn du die Vorschläge übernimmst, genügt ein „alle Vorschläge übernehmen“.

### Jetzt zu entscheiden: die Sicherheit

**1. Eigene Identität `a2a` oder der Operator?**

| | Möglichkeit | Folge |
|---|---|---|
| **A (Vorschlag)** | eigener Eintrag `a2a` im Server (Schlüssel `a2a.nk`), eigener Name im Subject | Rechner sehen den Absender `a2a`. Wer ihn nicht in `NATS_MCP_PUSH_FROM` führt, bekommt nichts in die Sitzung geschoben. Mehr Handgriffe: `viper a2a init`, ein zusätzlicher Name in `settingsfile` und `check` |
| B | die Brücke spricht als Operator | nichts Neues zu bauen, aber fremder Verkehr **erbt** das Vertrauen aus `NATS_MCP_PUSH_FROM=operator`: ein Aufrufer von außen landete direkt in der Sitzung eines Agenten |

*Begründung:* Die Sicherheit der Rechner hängt daran, **wer** in die Sitzung schreiben darf. Das darf nicht stillschweigend auf Fremde übergehen. Der Mehraufwand ist klein (Server und `natsop` tragen mehrere Operator-Einträge schon, §4.1).

**2. Wo läuft die Brücke?**

| | Möglichkeit | Folge |
|---|---|---|
| **A (Vorschlag)** | im Vordergrund auf dem Operator-Rechner (`viper a2a serve`, wie der Chat) | nutzt Tunnel und Schlüssel, die es gibt; nichts Neues auf Servern; endet mit Strg-C. Läuft nicht, wenn der Rechner aus ist |
| B | als Dienst auf dem NATS-Server oder in einem Container | dauerhaft erreichbar, aber ein weiterer Dienst, der Schlüssel, Netz und Updates braucht; mehr Angriffsfläche; `viper up` müsste ihn anlegen |

*Begründung:* Die Brücke ist neu und riskant. Sie klein zu halten und sichtbar zu betreiben (Vordergrund) ist besser als sie dauerhaft zu betreiben. B bleibt ein späterer Plan, wenn sich der Bedarf zeigt.

**5. Anmeldung der Aufrufer?**

| | Möglichkeit | Folge |
|---|---|---|
| **A (Vorschlag)** | **Bearer-Token**, Hash in einer Datei, je Token Name, erlaubte Rechner, Grenzen | einfach, jeder Client kann es; Token lässt sich einzeln widerrufen (`viper a2a token revoke`) |
| B | mTLS | stärker, aber Zertifikate für jeden Aufrufer, mehr Betrieb |
| C | OAuth/OpenID | nötig nur in Firmen mit Identitätsdienst; viel Umsetzung |

*Begründung:* Für eine Brücke, die der Betreiber selbst startet, ist ein Token je Aufrufer mit Rechnerliste das Passende. B und C lassen sich später ergänzen, die Karte nennt dann weitere `securitySchemes`.

**6. Wie weit ist die Brücke erreichbar?**

| | Möglichkeit | Folge |
|---|---|---|
| **A (Vorschlag)** | Standard `127.0.0.1`; eine andere Adresse nur mit TLS (`--tls-cert/--tls-key`) oder `--behind-proxy`, und nur mit Tokendatei | sicher als Standard; Clients von anderen Rechnern brauchen einen Tunnel, einen Proxy oder ein Zertifikat |
| B | gleich `0.0.0.0` | bequem, aber ein offener Port ins Internet für Text, der in Agenten landet |

*Begründung:* Die Brücke leitet Text in Agenten. Ein Fehler bei der Voreinstellung wäre schwer zu verzeihen.

**7. Muss ein Rechner die Brücke erlauben?**

| | Möglichkeit | Folge |
|---|---|---|
| **A (Vorschlag)** | ja, `NATS_A2A_ENABLED=true` je Rechner, Standard **aus** | wer die Brücke startet, macht damit keinen Rechner erreichbar, den er nicht bedacht hat; die Karte gibt es nur für erlaubte Rechner |
| B | alle Rechner der Gruppe sind erreichbar | weniger Einstellung, aber ein Versehen setzt alle Agenten einer Gruppe aus |

*Begründung:* Das Opt-in hat dieselbe Rolle wie `NATS_MCP_PUSH_FROM`: eine bewusste Zusage je Rechner.

**8. Kennzeile `[A2A: <Aufrufer>, Aufgabe <id>]` im Text?**

| | Möglichkeit | Folge |
|---|---|---|
| **A (Vorschlag)** | ja, feste erste Zeile, mit gültigem Namen des Aufrufers | der Agent sieht, dass der Text von außen kommt und von wem; **schützt nicht** vor Prompt-Injektion (der Plan sagt es) |
| B | Text unverändert weitergeben | der Agent kann Fremdtext von Text eines Rechners nicht unterscheiden |

*Begründung:* Die Zeile kostet nichts und hilft bei der Auswertung. Der echte Schutz bleiben 1, 5 und 7.

### Nach Meilenstein 0 zu entscheiden: hängt an einer Messung

**3. Welche Fassung von A2A?**

| | Möglichkeit | Folge |
|---|---|---|
| **A (Vorschlag)** | nur **1.0**; bei fehlendem Kopf `A2A-Version` (gilt nach der Spezifikation als 0.3) eine klare Fehlermeldung | die aktuelle Fassung, ein Dialekt weniger. Clients, die noch 0.3 sprechen, kommen zunächst nicht an |
| B | 1.0 und 0.3 | erreicht mehr Clients, aber zwei Formen von Methodennamen und Feldern, doppelte Tests |

*Begründung und Prüfung:* Der Vorschlag ist 1.0. In Meilenstein 0 messen wir, welche Clients und SDKs, die du wirklich nutzt, schon 1.0 sprechen. Sind es die meisten nicht, wird die Verträglichkeit mit 0.3 (Meilenstein 7) vorgezogen. **Das ist die Frage mit dem größten Einfluss auf den Nutzen der Brücke.**

**4. Welche Bindungen?**

| | Möglichkeit | Folge |
|---|---|---|
| **A (Vorschlag)** | JSON-RPC über HTTP zuerst; REST in Meilenstein 6; **kein gRPC** | JSON-RPC ist die verbreitete Form, HTTP+JSON kommt günstig dazu (dieselben Fälle). gRPC bräuchte Protobuf und viel Abhängigkeit |
| B | gleich alle drei | mehr Aufwand, unklarer Nutzen |

*Begründung:* Nur die Bindungen, die die Karte nennt, müssen laufen (*[?]* in Meilenstein 0 prüfen: die Spezifikation verlangt, so weit gelesen, nicht alle). gRPC lässt sich ergänzen, ohne den Kern zu ändern.

**9. Wie blockiert `SendMessage`?**

| | Möglichkeit | Folge |
|---|---|---|
| **A (Vorschlag)** | den HTTP-Aufruf offen halten, bis die Aufgabe beendet oder unterbrochen ist (höchstens eine Stunde) | entspricht der Spezifikation („MUST wait“). Clients und Proxys mit kurzen Fristen brechen ab; sie nehmen `SendStreamingMessage` oder `returnImmediately` |
| B | nach 120 Sekunden den Zustand zurückgeben und die Abweichung in Karte und Handbuch nennen | verträglich mit kurzen Fristen, aber nicht regelkonform; ein strenger Client kann meckern |

*Begründung und Prüfung:* A, weil die Brücke die Antwort ohnehin über den Strom zuordnet. Meilenstein 0 prüft, ob ein offener Aufruf über Minuten die Fristen von `net/http`, eines Proxys und eines echten Clients übersteht. Hält er nicht, wird B mit offener Abweichung genommen.

**10. Eigene Umsetzung oder Go-SDK?**

| | Möglichkeit | Folge |
|---|---|---|
| **A (Vorschlag)** | eigene schlanke Umsetzung (Typen, JSON-RPC, SSE, rund 800 Zeilen) | keine große Abhängigkeit, volle Kontrolle über Sicherheit und Fristen, wie bei MCP |
| B | das offizielle Go-SDK | weniger zu schreiben, aber `go.mod` wächst, und Sicherheit und Fassung folgen dem SDK |

*Begründung und Prüfung:* A, außer das SDK ist in Meilenstein 0 klein (nur Typen), unterstützt 1.0 und passt in die Schichten (`a2aweb` darf nur `port/driving` kennen). Gemessen werden `go.mod`/`go.sum`, Fassung, Lizenz.

### Reihenfolge

1. **Jetzt:** 1, 2, 5, 6, 7, 8 (zusammen das Sicherheitsmodell). Alle Vorschläge sind „sicher als Standard“.
2. **Dann Meilenstein 0**, der 3, 4, 9 und 10 mit Messungen beantwortet.
3. Danach Meilenstein 1.

## 10 Risiken

| Risiko | Gegenmaßnahme |
|---|---|
| Prompt-Injektion von außen in die Agenten | eigene Identität, Opt-in, Token je Aufrufer mit Rechnerliste, Kennzeile, kein `url`, `viper a2a serve` fragt bei `PUSH_FROM`; das Handbuch sagt es |
| Fremde Clients erwarten Fassung 0.3 | klare Fehlermeldung, Meilenstein 7; Beobachtung der Fassungen im Ökosystem |
| Geheimnisse in Antworten | Schwärzen wie `viper ui`; Opt-in je Rechner |
| Aufgaben bleiben offen, der Speicher wächst | Frist (1 Stunde), Alter und Größe begrenzt |
| Zustände täuschen mehr vor, als NATS weiß | Zustände nur nach §3.2; Handbuch nennt die Grenzen |
| Spezifikation ändert sich | Fassung in der Karte; `A2A-Version` ausgewertet; Abschnitt „was gelesen ist“ im Plan |
| Zwei Brücken gegen einen Schlüssel | Sperrdatei wie beim Chat |

### 10.1 Prüfung des Plans (20.11.2026)

Eine unabhängige Prüfung gegen die Spezifikation 1.0.0 und den Code ist eingearbeitet:

| Fund | Wo |
|---|---|
| Die Grenze von 120 Sekunden bei blockierendem `SendMessage` verletzt „MUST wait“; die Brücke braucht sie nicht | §3.3, §9.9 |
| `INPUT_REQUIRED` war nicht zuordenbar und fälschbar (kein Bezug zur Aufgabe, jeder Rechner darf `a2a` anschreiben); die späte Antwort geht beim Rechner verloren | §3.2.1 |
| Ein Anschluss an NATS fehlte für den Anwendungsfall (depguard); `RunCLI` je Aufgabe ist falsch; `natsmcp` ist nicht unverändert | §4.2, §6.3 |
| Mehrere Operator-Einträge tragen Server und `natsop` schon; zu ändern ist `cli` und die Einstellungen | §4.1, §6.3 |
| `watch --to` ist überflüssig | §4.2 |
| `WORKING` aus der Anwesenheit sagt „verbunden“, nicht „arbeitet“; die erste `res` ist endgültig; `REJECTED` nur für angelegte Aufgaben, sonst Fehler der Anfrage | §3.2 |
| Dateien: andere Arten werden abgelehnt, Speicher bei Base64, Gruppe ohne Objektspeicher, die Kennzeile zählt gegen 16 KiB | §3.3 |
| Anmeldung für jeden Pfad, Liste der Rechner angemeldet und gefiltert, gleiche Antwort für „gibt es nicht“ und „darf nicht“, Hash mit konstanter Zeit, `tenant` und Pfad | §2, §3.3, §5.2 |
| Kennzeile ist ein Hinweis, kein Schutz; Name des Aufrufers geprüft; Antworten markiert | §5.4 |
| Grenzen hinter einem Proxy (je Token-Name), `Host`-Prüfung, kein CORS, Ströme, Fristen; Download prüft die Aufgabe | §3.3, §5.2, §5.3 |
| Meilensteine: Tokenbefehle und die Frage nach `PUSH_FROM` schon in Meilenstein 1; Strom erst ab Meilenstein 3; Lauf gegen einen fremden Client in mehreren Abschnitten; CI auf drei Systemen | §8, §11 |
| Kleinigkeiten der Spezifikation: `supportedInterfaces` ist Pflicht, SSRF ist eine Empfehlung (SHOULD), Fehlercode `-32009`, „nur eine Bindung“ ist nicht belegt | §3.3, §5.5, §6.2, §7 |

## 11 Definition of Done

* `viper a2a init|serve|check|token|revoke` laufen am Terminal; ein fremder A2A-Client findet die Karte eines Rechners, stellt eine Aufgabe, bekommt die Antwort (auch spät und mit Rückfrage) und kann die Aufgabe ansehen.
* Ein Aufrufer ohne Token, mit falschem Token oder für einen anderen Rechner bekommt nichts; zwei Aufrufer sehen die Aufgaben des anderen nicht.
* Ohne `NATS_A2A_ENABLED` ist ein Rechner nicht erreichbar; der Name `a2a` ist für Rechner gesperrt.
* Kein Webhook, kein `url`: keine Anfrage der Brücke an eine Adresse, die ein Aufrufer nennt.
* Tests: Go-Tests (Zustände, Isolation, Grenzen), Tests mit eingebettetem NATS-Server und einem Rechner als Dienst, **Läufe gegen einen fremden Client in den Meilensteinen 0, 2, 3 und 5**; Lint und Browser-/HTTP-Tests grün in der CI auf Linux, macOS und Windows.
* Handbuch (Ist-Zustand, die Grenzen von NATS und A2A), README und Hilfe stimmen; **jeder Meilenstein unabhängig geprüft**.

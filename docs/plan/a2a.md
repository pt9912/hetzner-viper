# Plan: A2A-Brücke (`viper a2a`)

**Ziel:** Ein Agent, der **nicht** zur NATS-Gruppe gehört (ein anderes Framework, ein anderer Anbieter, ein Skript), soll einen Agenten auf einem Rechner der Gruppe über das offene **A2A-Protokoll** ansprechen können: Aufgabe stellen, Verlauf sehen, Rückfragen beantworten, Ergebnis und Dateien holen. `viper` übersetzt dabei A2A-Aufgaben in die NATS-Nachrichten, die es schon gibt (`request`, `reply`, Dateianhänge), und umgekehrt. Auf den Rechnern ändert sich nichts.

**Stand:** Entwurf (20.11.2026). Gebaut ist nichts. Die Entscheidungen in §9 sind **offen** und brauchen deine Antwort; zu jeder gibt es einen Vorschlag. *[V]* heißt: geprüft (A2A-Spezifikation 1.0.0 auf `a2a-protocol.org/latest/specification/`, Plan- und Code-Stand dieses Repositories); *[?]* ist eine Vermutung, die in Meilenstein 0 zu prüfen ist. **Die Spezifikation habe ich nicht Wort für Wort gelesen**, sondern mit gezielten Fragen zu Fassung, Bindungen, Operationen, Zuständen, Teilen, Agent-Card, Anmeldung, Strom, Push, Kennungen und Sicherheit ausgewertet; Einzelheiten (genaue Fehlercodes je Bindung, Feldlängen, Abschnitt 12 zu eigenen Bindungen) sind vor dem Bauen im Text nachzulesen.

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
| `WORKING` | Der Rechner ist laut Anwesenheit da (`here`) **und** die Frage ist raus. Ein Rechner ohne Anwesenheit bleibt `SUBMITTED` |
| `INPUT_REQUIRED` | Der Rechner hat der Brücke eine **Frage** gestellt (Umschlag `req` an `a2a`, `type=req` im Strom); der Text der Frage steht in `status.message`. Die Antwort des Aufrufers (neue Nachricht mit derselben `taskId`) geht als `reply --re` zurück |
| `COMPLETED` | Der Strom liefert `res` mit `re` = ID der Frage **vom Angefragten** an die Brücke; der Text und die Dateien werden zum Artefakt |
| `FAILED` | Die Frist der Aufgabe ist um (Standard 1 Stunde, §3.4), der Tunnel oder Server ist nicht erreichbar, oder die Frage wurde vom Server abgelehnt (Berechtigung, Größe). `status.message` nennt den Grund |
| `CANCELED` | Der Aufrufer hat `CancelTask` gerufen, bevor ein Ende kam. Auf dem Rechner wird nichts abgebrochen; eine spätere Antwort wird verworfen (der Rechner kann nicht gefragt werden, ob er sie noch braucht) |
| `REJECTED` | Die Brücke selbst hat die Nachricht **nicht angenommen** (zu groß, unbekannter Inhaltstyp, Aufrufer darf diesen Rechner nicht). Nichts geht nach NATS |
| `AUTH_REQUIRED` | wird nicht gesetzt (§7) |

Eine beendete Aufgabe nimmt keine Nachricht mehr an (`UnsupportedOperationError`); eine neue Nachricht im selben `contextId` ohne `taskId` ist eine **neue** Aufgabe *[V]*. `taskId` vergibt immer die Brücke *[V]*.

### 3.3 Operationen (Fassung 1.0.0 *[V]*)

| Operation | Umsetzung |
|---|---|
| Agent-Card | Je Rechner eine Karte unter `/<gruppe>/<rechner>/.well-known/agent-card.json` und eine Liste der Rechner unter `/<gruppe>/`. `supportedInterfaces`: JSON-RPC (und später HTTP+JSON) mit `protocolVersion` `1.0`, `tenant`. Pflichtfelder (`name`, `description`, `version`, `capabilities`, `defaultInputModes`, `defaultOutputModes`, `skills`) kommen aus den Einstellungen des Rechners (§4.3) und aus der Fassung von `viper` |
| `SendMessage` | Nachricht prüfen → Aufgabe anlegen (`SUBMITTED`) → `request --announce` an den Rechner (der Text ist der Text der Teile `text`, mit der Kennzeile von §5.4) → ohne `returnImmediately` (Standard: **blockierend**) warten, bis die Aufgabe `COMPLETED`, `FAILED` oder unterbrochen ist, höchstens **120 Sekunden**; danach die Aufgabe so zurückgeben, wie sie ist (`WORKING`), der Aufrufer fragt mit `GetTask` weiter *[?: die Spezifikation sagt „MUST wait until terminal or interrupted“; ob eine Obergrenze zulässig ist, ist in Meilenstein 0 zu klären; Ausweg: das Ergebnis als `Task` im Zustand `WORKING`, was jeder Client versteht]* |
| `SendStreamingMessage`, `SubscribeToTask` | SSE; Ereignisse nur an den Übergängen der Zustände (§3.2) und ein `artifactUpdate` mit der Antwort. Keine Teilergebnisse (NATS liefert die Antwort als **eine** Nachricht). `capabilities.streaming = true` |
| `GetTask`, `ListTasks` | aus dem Speicher der Brücke (§3.4); `historyLength` und Seiten (50 bis 100, Cursor, absteigend nach Zeit) wie in der Spezifikation; nur Aufgaben des **eigenen Aufrufers** |
| `CancelTask` | `CANCELED`, wenn nicht beendet; sonst `TaskNotCancelableError` |
| Push-Benachrichtigung (Webhook) | **nicht in Fassung 1** (§7): `capabilities.pushNotifications = false`, die Operationen antworten `PushNotificationNotSupportedError` |
| `GetExtendedAgentCard` | nicht gebaut (`ExtendedAgentCardNotConfiguredError`) |

**Teile (`Part`) nach innen:** `text` (Pflicht, sonst `REJECTED`), `raw` (Bytes, Base64) für PDF, ZIP und Bilder bis zur Grenze der Gruppe (`NATS_OBJECTS_FILE_MAX`, höchstens 64 MiB), Art nach den ersten Bytes wie auf den Rechnern (`natsmcp.Classify`, `CheckAttachment`); `data` (strukturiertes JSON) wird nicht unterstützt (`ContentTypeNotSupportedError`); **`url` wird abgelehnt** (die Brücke lädt nichts von Adressen, die ein Fremder nennt: SSRF, §5). Eine Datei je Nachricht (wie im Chat).

**Teile nach außen:** der Text der Antwort als `text`; eine Datei der Antwort (`att_*`) holt die Brücke mit `fetch` und liefert sie als `raw` bis zu einer kleinen Grenze (Vorschlag 4 MiB), größere als `url` auf einen **Pfad der Brücke** mit demselben Token (`/<gruppe>/<rechner>/files/<id>`), der die Datei immer als Download ausliefert (wie der Chat).

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

* Neu: `viper a2a init GRUPPE` trägt einen **zweiten Operator-Eintrag** mit dem Namen `a2a` (Einstellung `NATS_A2A_NAME`, Standard `a2a`) im Server ein; Schlüssel in `groups/<gruppe>/a2a.nk` (wie `operator.nk`, `0600`). Die Rechte sind die des Operators (`OperatorPermissions`): senden nur im eigenen Teilbaum `viper.<g>.a2a.>`, lesen die Gruppe. `viper a2a revoke GRUPPE` entfernt ihn. *[?: Ob der Server und `Register`/`Revoke`/`checkedClients` **mehrere** Operator-Einträge je Gruppe tragen, ist in Meilenstein 0 zu prüfen; die Bauart (`Client{Operator:true}` mit Namen) spricht dafür. Ist es nicht so, ist das eine kleine Änderung in `natsserver` und `natsop`.]*
* Der Name `a2a` ist für Rechner **gesperrt** (wie der Name des Operators, `settingsfile.Load`).

### 4.2 Schichten (nach dem Muster des Chats)

| Schicht | Inhalt |
|---|---|
| `internal/usecase/a2a` | Aufgaben, Zustände, Zuordnung von Zeilen des Stroms zu Aufgaben (`re`, Frist), Fragen der Rechner, Rechte je Aufrufer, Grenzen. Kein Netz |
| `internal/port/driving/a2a.go` | `A2A`-Schnittstelle (SendMessage, GetTask, ListTasks, CancelTask, Subscribe) und Typen |
| `internal/port/driven/a2astore` | Speicher der Aufgaben (Datei und Speicher als Adapter) |
| `internal/adapter/driving/a2aweb` | HTTP-Server: Agent-Cards, JSON-RPC, SSE, später REST; Anmeldung, TLS, Grenzen; **kein** Wissen über NATS |
| `internal/adapter/driving/cli` | `viper a2a serve|init|revoke`, der Anschluss an NATS (Tunnel, `RunCLI`), Strom über `chatstream.Connector` (vorhanden) |
| `internal/natsmcp` | unverändert; bei Bedarf eine Option `watch --to a2a` für nur die Zeilen an die Brücke *[?]* |

### 4.3 Einstellungen

Je Rechner (in den Einstellungen des Rechners, nur Beschreibung, keine Berechtigung): `NATS_A2A_ENABLED=true` (der Rechner wird als Agent angeboten; Standard **aus**), `NATS_A2A_DESCRIPTION` (Text der Karte), `NATS_A2A_SKILLS` (Liste `id:Name:Beschreibung`; ohne Angabe eine Fähigkeit `ask`: „Beantwortet eine Frage oder erledigt eine Aufgabe des Agenten auf diesem Rechner“). Je Gruppe: `NATS_A2A_NAME`. Die Berechtigungen der Aufrufer stehen **nicht** in den Einstellungen der Rechner (§5.2).

## 5 Sicherheitsmodell

### 5.1 Was die Brücke an Gefahren neu schafft

Ein Fremder, der die Brücke erreicht, kann Text in den Agenten eines Rechners bringen. Das ist die **Prompt-Injektion von außen** und dieselbe Gefahr wie bei `NATS_MCP_PUSH_FROM`, nur mit einem Netzwerk dazwischen. Darum: kein Zugriff ohne Anmeldung, Zustimmung des Betreibers je Rechner (`NATS_A2A_ENABLED`), eine Identität, die nicht der Operator ist, und die Kennzeile (§5.4).

### 5.2 Anmeldung und Berechtigung

* **Anmeldung:** Karte und Aufrufe verlangen ein **Bearer-Token** (`securitySchemes`: `HTTPAuthSecurityScheme`, Schema `bearer`). Die Tokens stehen in einer Datei der Brücke (`<Zustandsverzeichnis>/a2a/tokens`, `0600`): je Zeile Name des Aufrufers, **Hash** des Tokens, erlaubte Rechner (`gruppe/rechner` oder `*`), Grenzen (Aufgaben, Aufrufe je Minute). `viper a2a token add NAME --machines …` gibt das Token **einmal** aus. Die Agent-Card selbst ist öffentlich lesbar, wenn `--public-card` gesetzt ist; sonst auch sie mit Token. Weitere Verfahren (OAuth, OpenID, mTLS): nicht in Fassung 1 (§7).
* **Berechtigung:** je Aufrufer die Liste der Rechner. Eine Anfrage an einen anderen Rechner: `REJECTED`/404, ohne zu verraten, ob es ihn gibt.
* **Isolation:** `GetTask`, `ListTasks`, `CancelTask` und `SubscribeToTask` zeigen nur Aufgaben des **eigenen** Aufrufers; die Prüfung kommt **vor** jedem Zugriff auf den Speicher *[V: Spezifikation §13.1, MUST]*.
* **Grenzen je Aufrufer:** offene Aufgaben (Standard 4), Nachrichten je Minute (Standard 10), Größe (Text 16 KiB, Datei wie in §3.3). Überschritten: HTTP 429 und `REJECTED`.

### 5.3 Nach außen erreichbar machen

* Standard `--listen 127.0.0.1:PORT`. Eine andere Adresse verlangt `--tls-cert` und `--tls-key` **oder** ausdrücklich `--behind-proxy` (TLS endet davor; die Brücke vertraut dann `X-Forwarded-*` nicht für die Anmeldung). Ohne Tokendatei startet die Brücke bei einer fremden Adresse nicht.
* Die Karte nennt die **öffentliche Adresse** (`--public-url`), nicht die, an die gebunden ist.
* Das Handbuch rät zum Weg über einen vorhandenen Zugang (ssh-Tunnel, VPN, Reverse-Proxy mit TLS) statt eines offenen Ports.

### 5.4 Was an den Agenten geht

* Der Text beginnt mit **einer festen Zeile**: `[A2A: <Aufrufer>, Aufgabe <id>]`, dann ein Leerzeichen-Zeile, dann der Text des Aufrufers. So sieht der Agent, dass es Text von außen ist (und von wem), und kann ihn behandeln wie jede Nachricht eines anderen Agenten. Der Aufrufer kann die Zeile nicht ändern: Zeilen, die so beginnen, werden im Text des Aufrufers entschärft (ein Zeichen davor).
* Der Rechner sieht den Absender `a2a` (aus dem Subject, vom Server erzwungen). Nur wenn der Betreiber `a2a` in `NATS_MCP_PUSH_FROM` des Rechners führt, erscheint der Text in der Sitzung; sonst nur im Posteingang. **`viper a2a serve` fragt beim Start** (wie der Chat bei `NATS_MCP_PUSH_FROM`), wenn ein Rechner mit `NATS_A2A_ENABLED` den Namen `a2a` in der Liste führt, und verlangt `--yes`.

### 5.5 Was nicht geschützt ist

1. **Nichts ist privat in der Gruppe** (`nats-mcp.md` §3): jede Maschine liest die Frage des Aufrufers und die Antwort, auch Rechner, die A2A nicht anbieten. Das Handbuch sagt es; kein Vertrauliches über A2A, was die Rechner einander nicht lesen dürfen.
2. **Antworten gehen nach außen.** Die Brücke schwärzt bekannte Zugangsdaten (`[hidden]`, wie `viper ui`); ein Geheimnis ohne bekannten Namen kann herauskommen. Das ist die Wahl des Betreibers mit `NATS_A2A_ENABLED`.
3. **Ein übernommener Rechner** kann in seiner Antwort jeden Text an den Aufrufer schicken (Injektion in **dessen** Agenten). Die Brücke kann das nicht verhindern; sie liefert die Antwort als Text und nennt in `metadata` den Rechner.
4. **Wiederholung:** Ein Token, das gestohlen wird, ist bis zum Widerruf gültig (`viper a2a token revoke`).
5. **SSRF:** Es gibt keine Webhooks und keine Teile mit `url`; die Brücke ruft keine Adresse auf, die ein Aufrufer nennt *[V: Spezifikation §13.2 verlangt dort Prüfungen; wir lassen die Funktion weg]*.

### 5.6 Protokoll und Spuren

* `events.log` bekommt eine Zeile je Aufgabe (Zeit, Aufrufer, Rechner, Zustand am Ende), **ohne** Inhalt. Anmeldefehler werden gezählt und begrenzt (Brute-Force: nach 10 Fehlern je Adresse eine Minute Pause).
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

* **Fassung 1.0** (`protocolVersion: "1.0"`). Der Kopf `A2A-Version` wird ausgewertet: `1.0` gilt; **fehlt er, gilt nach der Spezifikation 0.3**, und die Brücke antwortet `VersionNotSupportedError` mit einem Hinweis. Eine Verträglichkeit mit 0.3 (andere Methodennamen und Felder) ist Meilenstein 7.
* **Bindung:** JSON-RPC über HTTP (`POST /<gruppe>/<rechner>/rpc`, PascalCase-Methoden) und SSE für Ströme. HTTP+JSON/REST ist Meilenstein 6, gRPC ist nicht geplant (§7).
* **Bibliothek:** Eine eigene schlanke Umsetzung (Typen, JSON-RPC, SSE, rund 800 Zeilen) oder das offizielle Go-SDK von A2A; in Meilenstein 0 gemessen an Größe (`go.mod`, `go.sum`), Fassung 1.0, Lizenz und Anschluss an die Schichten. Vorschlag: eigene Umsetzung, wie bei MCP (`nats-mcp.md` §2), außer das SDK ist klein und hat nur die Typen.

### 6.3 Änderungen am vorhandenen Code

| Stelle | Änderung |
|---|---|
| `internal/natsserver`, `internal/usecase/natsop` | mehrere Operator-Einträge je Gruppe (Name `a2a`); Test |
| `internal/adapter/driving/cli` | Befehle `a2a …`, Hilfe, Befehlstabelle; `loadNatsGroup` kennt die Identität `a2a` (Schlüsseldatei, Name); `openOperatorDead` mit Name statt festem Operator |
| `internal/natsmcp` | evtl. `watch --to NAME`; sonst nichts |
| `internal/model` | Namen `a2a` gesperrt; Typen der Aufgaben |
| Neue Pakete | `usecase/a2a`, `port/driving`, `port/driven/a2astore`, `adapter/driven/a2astore`, `adapter/driving/a2aweb` |
| Handbuch, README, Hilfe | Kapitel „Rechner über A2A erreichbar machen“ (Ist-Zustand), Befehlstabelle |

## 7 Bewusst nicht enthalten (Fassung 1)

* **gRPC-Bindung** (braucht Protobuf und viel Abhängigkeit; die Spezifikation verlangt nur eine Bindung).
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
| 0 | **Verifikation:** mehrere Operator-Einträge im Server; Strom nur mit Zeilen an `a2a`; blockierendes `SendMessage` mit Obergrenze (Spezifikation `MUST wait`); Fassung 1.0 gegen einen **fremden Client** (ein A2A-SDK oder der Prüfer der Gemeinschaft) mit einer Handvoll Karten und einer Nachricht; Größe und Fassung des Go-SDK; die Teile der Spezifikation, die nur über Fragen ausgewertet sind (Fehlercodes je Bindung, Abschnitt 12) | Messungen im Plan, Entscheidung zu Bibliothek, Version 0.3 und Obergrenze |
| 1 | **Gerüst:** `viper a2a init|revoke|serve|check`, Identität `a2a`, Agent-Cards je Rechner, Anmeldung mit Token, `127.0.0.1`, Sperrdatei | Go-Tests (Karte, Anmeldung, Name gesperrt), Browser-/HTTP-Tests mit dem echten Programm |
| 2 | **Aufgaben:** `SendMessage` blockierend → `request --announce`, Speicher, `GetTask`, `ListTasks`, `CancelTask`, Zustände, Fristen, Isolation | Tests mit eingebettetem NATS-Server (Rechner als Dienst), Tabellentests der Zustände, Isolation zweier Aufrufer |
| 3 | **Strom und Rückfragen:** `SendStreamingMessage`, `SubscribeToTask` (SSE), späte Antworten über `re`, `INPUT_REQUIRED` mit `reply` | Tests mit Verzögerungen, Neustart der Brücke mit offener Aufgabe |
| 4 | **Dateien:** `raw` nach innen (Prüfung wie die Rechner), `raw`/`url` nach außen mit `fetch`, Download-Pfad | Tests der Grenzen, Kopfzeilen des Downloads |
| 5 | **Härten:** TLS, `--behind-proxy`, Grenzen je Aufrufer, 429, Schwärzen, Kennzeile, `events.log`, Brute-Force-Bremse, `viper a2a token …` | Tests der Angriffe (§5), Fuzz von JSON-RPC, Prüfung durch Dritte |
| 6 | **HTTP+JSON/REST** (`/message:send` usw.) | dieselben Fälle über REST |
| 7 | **Handbuch, README, Hilfe** (Ist-Zustand, keine Chronik); optional **Verträglichkeit mit 0.3** | Durchsicht, unabhängige Prüfung |

Nach jedem Meilenstein: unabhängige Prüfung, wie bei `viper ui` und beim Chat. Die Brücke braucht **keine** neuen Rechte auf den Rechnern, nur den zusätzlichen Eintrag im Server.

## 9 Entscheidungen (offen)

1. **Eigene Identität `a2a` oder der Operator?** Vorschlag: eigene Identität (§2 Regel 1, §4.1). Der Operator brächte kein neues Konzept, aber fremder Verkehr erbte sein Vertrauen in `NATS_MCP_PUSH_FROM`.
2. **Wo läuft die Brücke?** Vorschlag: auf dem Operator-Rechner, im Vordergrund (wie der Chat). Alternativ als Dienst auf dem NATS-Server oder in einem Container: mehr Betrieb, mehr Angriffsfläche, keine Verwendung von Tunnel und Schlüssel des Operators.
3. **Welche Fassung?** Vorschlag: nur 1.0, mit klarer Fehlermeldung bei 0.3 (Meilenstein 7 optional). Viele vorhandene Clients sprechen noch 0.3; das ist die größte Reibung mit dem Ökosystem.
4. **Welche Bindungen?** Vorschlag: JSON-RPC zuerst, REST in Meilenstein 6, gRPC nicht.
5. **Anmeldung:** Vorschlag: Bearer-Token mit Hash und Rechnerliste je Aufrufer; mTLS und OAuth später.
6. **Standard der Reichweite:** Vorschlag: `127.0.0.1`. Eine fremde Adresse nur mit TLS und Token.
7. **Opt-in je Rechner (`NATS_A2A_ENABLED`):** Vorschlag: ja, Standard aus.
8. **Kennzeile `[A2A: …]` im Text:** Vorschlag: ja (§5.4). Gegenargument: der Text ändert sich; dafür weiß der Agent, woher er kommt.
9. **Obergrenze von 120 Sekunden bei `SendMessage`:** Vorschlag: zurückgeben, was da ist (`WORKING`). Vorbehalt: die Spezifikation sagt „MUST wait“ (Meilenstein 0).
10. **Eigene Umsetzung oder Go-SDK?** Vorschlag: eigene schlanke Umsetzung, falls das SDK in Meilenstein 0 mehr als die Typen mitbringt.

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

## 11 Definition of Done

* `viper a2a init|serve|check|token|revoke` laufen am Terminal; ein fremder A2A-Client findet die Karte eines Rechners, stellt eine Aufgabe, bekommt die Antwort (auch spät und mit Rückfrage) und kann die Aufgabe ansehen.
* Ein Aufrufer ohne Token, mit falschem Token oder für einen anderen Rechner bekommt nichts; zwei Aufrufer sehen die Aufgaben des anderen nicht.
* Ohne `NATS_A2A_ENABLED` ist ein Rechner nicht erreichbar; der Name `a2a` ist für Rechner gesperrt.
* Kein Webhook, kein `url`: keine Anfrage der Brücke an eine Adresse, die ein Aufrufer nennt.
* Tests: Go-Tests (Zustände, Isolation, Grenzen), Tests mit eingebettetem NATS-Server und einem Rechner als Dienst, ein Lauf gegen einen fremden Client; Lint und Browser-/HTTP-Tests grün in der CI auf Linux, macOS und Windows.
* Handbuch (Ist-Zustand, die Grenzen von NATS und A2A), README und Hilfe stimmen; **jeder Meilenstein unabhängig geprüft**.

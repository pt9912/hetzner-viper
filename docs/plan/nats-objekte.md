# Plan: Bilder und PDFs in NATS-Gruppen (Object Store)

**Ziel:** Ein Agent oder der Betreiber schickt einer Maschine (oder einem Thema) ein **Bild** oder ein **PDF**. Die Datei liegt im **Object Store** des NATS-Servers der Gruppe, die Nachricht trägt nur eine Referenz. Der Empfänger holt die Datei mit einem Werkzeug des MCP-Servers (`viper-nats-mcp`) in ein Verzeichnis auf seiner Maschine und liest sie von dort.

**Stand:** Entwurf (14.11.2026), noch nichts gebaut, noch nicht geprüft. Alles, was als *[?]* steht, ist eine Vermutung aus dem Wissen über `nats-server` und `nats.go` und wird in Meilenstein 0 mit einem echten Server geprüft, bevor etwas darauf gebaut wird. *[V]* heißt: im Code dieses Repositorys gelesen.

Grundlage: [`nats.md`](nats.md) (Gruppe, Rechte, „JetStream wird nicht aktiviert“, §Persistenz), [`nats-mcp.md`](nats-mcp.md) (der MCP-Server, die Werkzeuge) und [`nats-cli.md`](nats-cli.md) (`viper nats`).

## 1 Ausgangslage

* Die Nachricht ist heute nur Text: `Envelope{V, Kind, ID, Re, Text, State, Query}`, ein Text hat höchstens 16 KiB, die kodierte Nachricht 32 KiB (`maxWire`) *[V]*. Unbekannte Felder lehnen den Empfänger ab (strenges Lesen, `checkKeys`) *[V]*.
* **JetStream ist ausgeschaltet, mit Absicht:** `natscheck.sh` bricht mit „JetStream is on, it must not be“ ab *[V]*, `nats.md` legt es fest. Der Object Store läuft auf JetStream. Dieser Plan hebt die Festlegung **nur für Gruppen auf, die es ausdrücklich wollen** (`NATS_OBJECTS=true`).
* Eine Maschine darf nur in ihren Bereich `viper.<g>.<m>.>` und in `viper.<g>.*.req.>` senden und `viper.<g>.>` und `_INBOX.<m>.>` lesen *[V]* (`natsserver.Permissions`). `allow_responses` ist bewusst nicht gesetzt.
* Der Server läuft als Prozess im Heimverzeichnis der Server-Maschine, `/home/builder/.viper-nats` *[V]* (Pod und Container: Volume `<name>-home`; VM: die Platte). Die systemd-Unit der VM setzt `ProtectHome=read-only` und `ReadOnlyPaths=<Dir>` *[V]*: Ein `store_dir` braucht dort ein `ReadWritePaths`.

## 2 Entscheidungen

| Thema | Festlegung |
|---|---|
| Einschalten | `NATS_OBJECTS=true` in der **Gruppendatei** (Standard: aus). Ohne die Einstellung ändert sich nichts: JetStream bleibt aus, `natscheck.sh` bleibt streng, kein neues Recht. Die Einstellung wird beim Einrichten des **Servers** in `server.conf` festgehalten; ein Client mit einer anderen Einstellung wird gewarnt (wie bei den anderen Gruppeneinstellungen). |
| Grenzen | `NATS_OBJECTS_MAX` (Gesamtgröße des Speichers, Standard 256 MiB), `NATS_OBJECTS_FILE_MAX` (je Datei, Standard 8 MiB), `NATS_OBJECTS_TTL` (Aufbewahrung, Standard 7 Tage). Sie werden als `max_file_store` des Servers und `max_bytes`/`max_age` des Streams durchgesetzt, die Dateigröße zusätzlich im Client. |
| Ort | `store_dir` im Heimverzeichnis der Server-Maschine (`<Dir>/js`). Die Dateien überstehen einen Neustart des Pods, des Containers und der VM, weil das Heimverzeichnis ein Volume oder die Platte ist. Sie gehen mit `viper down` der Server-Maschine verloren, und wenn die Rolle des Servers auf eine andere Maschine wechselt (§8). |
| Bucket | **Ein** Bucket je Gruppe, `viper-<gruppe>` (Stream `OBJ_viper-<gruppe>`). **`viper` legt ihn selbst an** (mit dem Schlüssel des Servers beim Einrichten, mit fester Konfiguration); die Maschinen dürfen ihn **nicht anlegen, ändern oder löschen**. |
| Namen | Der Name des Objekts ist eine **zufällige ID** (32 Hexzeichen), die der Sender wählt; nie ein Dateiname des Senders. Es wird nie überschrieben. Der Dateiname des Senders wandert nur als bereinigte Anzeige in die Referenz. |
| Löschen | Keine Maschine löscht oder leert etwas: Dateien verfallen nach der TTL (`max_age`) oder, wenn der Speicher voll ist, die ältesten zuerst (`discard: old`). Das vermeidet das Recht auf `$JS.API.STREAM.PURGE` und `…MSG.DELETE`, das eine Maschine den ganzen Bucket leeren ließe (Vermutung, §4). |
| Typen | Nur **PDF** (`application/pdf`) und **Bild** (PNG, JPEG, GIF, WebP). Der Typ wird an den **ersten Bytes** geprüft, vom Sender beim Senden und vom Empfänger beim Holen; die Endung und eine Angabe des Senders zählen nicht. |
| Prüfsumme | SHA-256 des Inhalts in der Referenz; der Empfänger prüft sie nach dem Holen. |

## 3 Der Server

* `server.conf` bekommt bei `NATS_OBJECTS=true`: `jetstream { store_dir: "<Dir>/js", max_file_store: <MAX> }`. Ohne: nichts, wie heute. `Render` lehnt Größen und Pfade außerhalb des Rahmens ab.
* **VM:** `ReadWritePaths=<Dir>/js` in der Unit (nur dann). Pod und Container: das Heimverzeichnis ist schreibbar *[V]*.
* **Anlegen des Buckets:** beim Einrichten des Servers (und bei jedem `viper up` des Servers, gleich bleibend: nur anlegen, wenn er fehlt, nichts ändern, was von Hand geändert wurde, aber melden). Konfiguration: `storage: file`, `replicas: 1`, `max_bytes`, `max_age`, `discard: old`, `max_msg_size` etwas über einem Stück (128 KiB), `allow_rollup_hdrs` aus *[?]*, ein Stück je Nachricht.
* **`natscheck.sh`:** nimmt den neuen Parameter `objects=on|off`. Mit `off` bleibt die Prüfung „JetStream darf nicht an sein“. Mit `on` verlangt sie das Gegenteil und prüft, dass der Bucket da ist.
* **`viper check`:** vergleicht die Einstellung der Gruppe mit der in `server.conf` und meldet Abweichungen (wie bei `NATS_MCP_TOPICS`).

## 4 Die Rechte *[?]*

Das ist der Teil, der Meilenstein 0 braucht. Ziel: eine Maschine kann **Dateien ablegen und holen, aber nichts anlegen, ändern, löschen oder leeren**. Vermutete Menge je Maschine (`<b>` = `viper-<gruppe>`):

| Was | Veröffentlichen | Lesen |
|---|---|---|
| Ablegen (`Put`) | `$O.<b>.C.*` (Stücke), `$O.<b>.M.*` (Metadaten), `$JS.API.STREAM.INFO.OBJ_<b>` | die Antworten auf `_INBOX.<m>.>` (schon erlaubt) |
| Holen (`Get`) | `$JS.API.CONSUMER.CREATE.OBJ_<b>` und `…CREATE.OBJ_<b>.>`, `$JS.API.CONSUMER.DELETE.OBJ_<b>.*` *[?]*, `$JS.API.STREAM.INFO.OBJ_<b>` | `_INBOX.<m>.>` für die Antworten und die Zustellung |
| Nicht erlaubt | `$JS.API.STREAM.CREATE/UPDATE/DELETE/PURGE.OBJ_<b>`, `$JS.API.STREAM.MSG.DELETE.OBJ_<b>`, jede andere Gruppe | |

* Der Operator bekommt dieselbe Menge (er sendet über einen SSH-Tunnel, siehe `nats-cli.md`).
* **Zu prüfen:** (a) reicht `Put` ohne `STREAM.PURGE`, wenn kein Name überschrieben wird *[?]*; (b) holt `Get` mit einem **geordneten Consumer** ohne `CONSUMER.DELETE`; (c) was passiert, wenn eine Maschine ihren Bereich verlässt (der Server meldet `Permissions Violation`, `settle` zeigt es); (d) die kleinste Menge, mit der das Programm `nats` und `nats.go` (`jetstream.ObjectStore`) arbeiten; (e) ob `$O.<b>.M.*` einen Namen nach dem Schema `viper-<g>` erzwingen kann; (f) ob eine Maschine den Bucket anderer Gruppen sieht (nein, wenn die Subjects den Gruppennamen tragen).
* **Offene Folge:** Alle Maschinen der Gruppe können alle Dateien der Gruppe holen. Das entspricht „in einer Gruppe ist nichts privat“ (`nats.md`).

## 5 Die Nachricht

Neues Feld im Umschlag (`json:"att,omitempty"`):

```json
{"v":1,"kind":"msg","text":"Bitte ansehen","att":{"id":"<32 hex>","type":"pdf","name":"bericht.pdf","size":123456,"sha256":"<64 hex>"}}
```

* `type` ist `pdf`, `png`, `jpeg`, `gif` oder `webp`; `name` ist bereinigt (nur Anzeige, höchstens 64 Zeichen, keine Pfadtrenner, keine Steuerzeichen); `id` ist der Objektname; Größe und Prüfsumme begrenzt und geprüft. Ein Anhang gehört zu `msg` und `req`, nicht zu `res`, `presence`, `topics`.
* **Älteres `viper-nats-mcp` lehnt die ganze Nachricht ab** (unbekanntes Feld). Der Absender kann das nicht wissen: Anhänge gehen erst, wenn **alle** Empfänger die neue Version haben. Das steht im Handbuch; `viper check` meldet eine Maschine mit alter Version (Version des Binaries steht schon in `connection.env`/`viper check` *[?]*).
* Subject und Themen ändern sich nicht, `maxWire` und `MaxText` bleiben.

## 6 Der MCP-Server

* `send`, `broadcast` und `request` bekommen den Parameter `attachment` (Pfad einer Datei **auf dieser Maschine**, höchstens `NATS_OBJECTS_FILE_MAX`). Der MCP-Server prüft Typ und Größe, legt die Datei im Bucket ab und sendet die Nachricht mit der Referenz. Der Pfad muss unterhalb des Heimverzeichnisses liegen, kein Link auf etwas außerhalb *[?]*.
* **Neues Werkzeug `fetch`** (`id`): holt das Objekt, prüft Größe, Prüfsumme und Typ an den ersten Bytes und legt es unter `<Dir>/attachments/<id>.<endung>` ab (Modus 0600). Es antwortet mit Pfad, Typ und Größe, **nie mit dem Inhalt**. Der Agent liest die Datei danach mit seinen eigenen Mitteln (Claude Code kann Bilder und PDFs aus Dateien lesen).
* `inbox` und der Push nennen einen Anhang als eine Zeile („Anhang: bericht.pdf, pdf, 120 KiB, id …“), nicht mehr. **Es wird nichts automatisch geholt oder geöffnet.**
* Der Ordner `attachments` hat eine Obergrenze (zum Beispiel 64 MiB und 50 Dateien); die ältesten werden gelöscht, wenn eine neue dazukommt.
* `fetch` ist **nicht** in `NATS_MCP_AUTOALLOW=read`: Es schreibt eine Datei. Es gehört zu `all` (und zur Liste der Werkzeuge in `model.MCPTools()` und `viper check`).

## 7 `viper nats`

* `viper nats send|broadcast|request GRUPPE … --pdf DATEI` und `--image DATEI` (genau eine von beiden oder keine; der Text darf dazukommen). `viper` prüft Typ und Größe lokal, **bevor** es sich verbindet.
* **Als Maschine** (ohne eigenen Schlüssel): Die Datei geht über die **Standardeingabe** der SSH-Verbindung an `viper-nats-mcp` (`attach-put`); der Text bleibt ein Argument oder kommt aus `--file`. Die Standardeingabe trägt heute den Text *[V]*; sie trägt dann einen kleinen Kopf (Typ, Größe) und die Bytes. Das ist neu zu entwerfen.
* **Als Betreiber:** direkt über den Tunnel mit dem Schlüssel des Betreibers.
* `listen` zeigt eine Nachricht mit Anhang mit der Referenz; **`viper nats fetch GRUPPE ID [--out VERZEICHNIS]`** holt sie auf Ihren Rechner.

## 8 Grenzen und Risiken

* **Lebensdauer:** Dateien leben, solange das Heimverzeichnis der Server-Maschine lebt. `viper down` der Server-Maschine, ein anderer Server oder ein neues Volume löschen sie; ein Empfänger, der später holt, bekommt „nicht gefunden“. Das gehört ins Handbuch.
* **Fremde Inhalte:** Ein Bild oder PDF eines anderen Agenten kann Anweisungen enthalten (Text im Bild, versteckter Text im PDF) und aktive Inhalte (PDF). Kein Filter kann das sicher entfernen. Darum: nichts wird automatisch geholt, geöffnet oder in die Sitzung geschoben; der Typ wird an den Bytes geprüft; das Handbuch sagt es deutlich.
* **Pfad und Namen:** Nichts vom Sender wird zu einem Pfad; Dateiname = `<id>.<endung>` mit fester Endung je Typ.
* **Speicher:** `max_file_store` und TTL schützen den Server. Ein Sender kann den Speicher mit vielen Dateien füllen (die ältesten fallen heraus): Das ist ein Angriff auf andere Anhänge, kein Ausfall des Servers. Das Limit je Datei und die TTL begrenzen es nur.
* **Rechte:** Der Plan nimmt den Bucket als gemeinsam an. Wer in der Gruppe ist, holt alles. Wer Rechte auf `$JS.API.STREAM.PURGE` oder `…DELETE` hätte, leerte ihn; §4 verweigert beides.
* **Zuwachs der Fläche:** JetStream ist eine zusätzliche Angriffsfläche am Server. Er läuft nur mit `NATS_OBJECTS=true`, hinter TLS und Anmeldung wie bisher, mit den Rechten aus §4.
* **Windows, ältere Server:** Der MCP-Server läuft auf den Maschinen (Linux); `viper nats --pdf` liest eine lokale Datei auf jedem Betriebssystem.

## 9 Meilensteine

0. **Prüfen (nichts bauen):** Mit dem `nats-server` aus `go.mod` und `nats.go` (`jetstream.ObjectStore`) die Rechte aus §4 ausprobieren: die kleinste Menge für `Put` und `Get`, und dass `STREAM.PURGE/DELETE/UPDATE/CREATE` verweigert werden und trotzdem kein Name überschrieben werden muss. Ergebnis zurück in §4.
1. **Server:** `NATS_OBJECTS*`-Einstellungen, `Render`, Unit, Anlegen des Buckets, `natscheck.sh`, `viper check`.
2. **Rechte:** Die Menge aus Meilenstein 0 in `Permissions` und `OperatorPermissions` (nur bei `NATS_OBJECTS=true`) und die Tests.
3. **Nachricht und MCP-Server:** `att` (Lesen und Prüfen, strenge Grenzen), die Parameter von `send`/`broadcast`/`request`, das Werkzeug `fetch`, `inbox`, Obergrenze des Ordners, `MCPTools()`, `viper check`, Tests (auch gegen einen echten Server).
4. **CLI:** `--pdf`/`--image`, die Standardeingabe als Weg der Datei, `viper nats fetch`, Betreiber über den Tunnel.
5. **Handbuch und Läufe:** Handbuch (NATS-Kapitel, Sicherheit, Grenzen), `developer.md`, ein Lauf mit drei Maschinen gegen echtes Docker (Compose) und, wenn möglich, gegen k3d.

## 10 Offene Fragen

* Ein Bucket je Gruppe, oder je Absender (damit nur der Absender seine Dateien überschreibt)? Der Plan nimmt einen und verbietet das Überschreiben.
* Reicht die TTL von 7 Tagen, und reicht ein Anhang je Nachricht?
* Soll `fetch` auch einen Anhang aus einer Gruppe holen dürfen, in der die Maschine nicht Mitglied ist? (Nein: der Bucket ist der der eigenen Gruppe.)
* Soll `viper nats fetch` ohne Schlüssel des Betreibers über eine Maschine laufen (SSH, `fetch`, `scp` zurück)? Der Plan lässt es zunächst nur für den Betreiber zu.

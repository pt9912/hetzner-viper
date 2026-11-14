# Plan: Bilder und PDFs in NATS-Gruppen (Object Store)

**Ziel:** Ein Agent oder der Betreiber schickt einer Maschine (oder einem Thema) ein **Bild**, ein **PDF** oder ein **ZIP-Archiv**. Die Datei liegt im **Object Store** des NATS-Servers der Gruppe, die Nachricht trägt nur eine Referenz. Der Empfänger holt die Datei mit einem Werkzeug des MCP-Servers (`viper-nats-mcp`) in ein Verzeichnis auf seiner Maschine und liest sie von dort.

**Stand:** Entwurf (14.11.2026), einmal unabhängig geprüft, die Rechte und das Verhalten bei Ablauf und vollem Speicher sind mit `nats-server` 2.15.2 und `nats.go` 1.55.0 gemessen (§4, §11); noch nichts gebaut. *[V]* heißt: im Code dieses Repositorys gelesen oder gemessen. *[?]* ist eine offene Vermutung.

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
| Grenzen | `NATS_OBJECTS_MAX` (Gesamtgröße des Speichers, Standard 256 MiB), `NATS_OBJECTS_FILE_MAX` (je Datei, Standard 8 MiB), `NATS_OBJECTS_TTL` (Aufbewahrung, Standard 7 Tage). `max_age` am Stream lässt Stücke und Metadaten **zusammen** ablaufen; `max_file_store` am Server und `max_bytes` und `max_msgs` am Stream sind die harte Obergrenze (§3), die Dateigröße wird zusätzlich im Client geprüft. **Kein `discard: old`**: Gemessen verdrängt es zuerst die Stücke und lässt die Metadaten stehen, die Datei ist dann kaputt („received a corrupt object, digests do not match“). Ist der Speicher voll, **scheitert das Ablegen** mit einer klaren Meldung, bis Dateien ablaufen. |
| Ort | Pod und Container: `store_dir` im Heimverzeichnis der Server-Maschine (`<Dir>/js`, das Volume `<name>-home`). **VM:** außerhalb von `<Dir>`, `/var/lib/viper-nats` mit `StateDirectory=` in der Unit (die Unit sperrt `<Dir>` schreibend; §3). Die Dateien überstehen Neustarts. Sie gehen mit `viper down` der Server-Maschine verloren, und wenn die Rolle des Servers auf eine andere Maschine wechselt (§8). |
| Bucket | **Ein** Bucket je Gruppe, `viper-<gruppe>` (Stream `OBJ_viper-<gruppe>`). **`viper` legt ihn selbst an** (mit dem Schlüssel des Servers beim Einrichten, mit fester Konfiguration); die Maschinen dürfen ihn **nicht anlegen, ändern oder löschen**. |
| Namen | Der Name des Objekts ist eine **zufällige ID** (32 Hexzeichen), die der Sender wählt; nie ein Dateiname des Senders. **Dass nie überschrieben wird, erzwingt der Server nicht:** Wer `$O.<b>.M.*` und `$O.<b>.C.*` schreiben darf, kann Metadaten und Stücke **jedes** Objekts ersetzen oder fälschen (gemessen, §11). Das ist ein bekanntes Restrisiko in einer Gruppe, in der „nichts privat“ ist; die **Prüfsumme in der Nachricht** (Absender ist durch das Subject bewiesen) ist der Schutz: Der Empfänger verwirft eine Datei, deren SHA-256 nicht passt. Der Dateiname des Senders wandert nur als bereinigte Anzeige in die Referenz. |
| Löschen | Keine Maschine löscht, leert oder ändert den Stream: Die Rechte auf `STREAM.PURGE`, `…DELETE`, `…UPDATE`, `…CREATE` und `MSG.DELETE` fehlen (gemessen: verweigert, `ob.Delete` scheitert). Dateien verfallen nach der TTL. `deny_purge` und `deny_delete` am Stream gehen **nicht**: Der Object Store braucht Roll-ups für seine Metadaten (`err_code=10052`). |
| Typen | Nur **PDF** (`application/pdf`), **Bild** (PNG, JPEG, GIF, WebP) und **ZIP** (`application/zip`). Der Typ wird an den **ersten Bytes** geprüft, vom Sender beim Senden und vom Empfänger beim Holen; die Endung und eine Angabe des Senders zählen nicht. Ein `.docx`, `.xlsx` oder `.jar` ist technisch ein ZIP und geht als `zip` durch. |
| ZIP-Prüfung | Bei `zip` liest `viper` **nur das Inhaltsverzeichnis** (Go `archive/zip`, ohne zu entpacken), beim Senden und beim Holen, und lehnt ab bei: mehr als 1000 Einträgen; mehr als 64 MiB entpackter Größe laut Verzeichnis oder einem Verhältnis über 100:1; Namen mit `..`, mit absolutem Pfad oder Laufwerksbuchstaben, mit NUL oder Steuerzeichen; Verknüpfungen; verschlüsselten Einträgen; einem Verzeichnis, das nicht zur Datei passt. **`viper` entpackt nie.** Die Angaben im Verzeichnis kann ein Angreifer fälschen (die echte Größe kennt erst das Entpacken), das ist eine Vorprüfung und keine Garantie (§8). |
| Prüfsumme | SHA-256 des Inhalts in der Referenz; der Empfänger prüft sie nach dem Holen. |

## 3 Der Server

* `server.conf` bekommt bei `NATS_OBJECTS=true`: `jetstream { store_dir: "<Verzeichnis>", max_file_store: <MAX> }`. Ohne: nichts, wie heute. `Render` lehnt Größen und Pfade außerhalb des Rahmens ab. `max_file_store` begrenzt den Server insgesamt und gehört in die Größe der VM-Platte.
* **VM:** Die Unit setzt `ProtectSystem=strict`, `ProtectHome=read-only` und `ReadOnlyPaths=<Dir>` *[V]*. Darum liegt der Speicher dort **außerhalb** von `<Dir>`: `StateDirectory=viper-nats` (Verzeichnis `/var/lib/viper-nats`), kein `ReadWritePaths` unter einem schreibgeschützten Pfad (die Reihenfolge der Zeilen wäre sonst entscheidend). Pod und Container: das Heimverzeichnis ist schreibbar *[V]*, `<Dir>/js`.
* **Anlegen des Buckets:** beim Einrichten des Servers und bei jedem `viper up` des Servers: anlegen, wenn er fehlt; nichts ändern, was von Hand geändert wurde, aber melden. Konfiguration: `storage: file`, `replicas: 1`, `max_bytes`, `max_age`, **`max_msgs`** (sonst füllen viele winzige Objekte den Stream: gemessen 582 Nachrichten bei 100 KiB, jede mit eigenem Subject), `discard: new` (Standard des Object Stores, bleibt), `max_msg_size` mindestens **256 KiB** oder nicht gesetzt (ein Stück ist 128 KiB plus Kopfzeilen; 128 KiB wäre zu klein). `max_payload` des Servers ist 1 MiB *[V]*, die Stückgröße 128 KiB *[V]*.
* **Voller Speicher:** Ein Ablegen, das scheitert, meldet `maximum bytes exceeded` (`err_code=10077`); es lässt **Teilstücke** liegen, die erst mit der TTL gehen. Der Absender meldet das als „Speicher der Gruppe voll“ und sendet die Nachricht **nicht**.
* **`natscheck.sh`:** nimmt den neuen Parameter `objects=on|off`. Mit `off` bleibt die Prüfung „JetStream darf nicht an sein“ (beide Zustände sind erlaubt, je nach Einstellung). Mit `on` verlangt sie das Gegenteil und prüft die **Konfiguration** des Streams (Grenzen, `discard`), nicht nur, dass der Bucket da ist.
* **`viper check`:** vergleicht die Einstellung der Gruppe mit der in `server.conf` und meldet Abweichungen (wie bei `NATS_MCP_TOPICS`).

## 4 Die Rechte *[V]*

Gemessen mit einem eingebetteten `nats-server` 2.15.2, `nats.go` 1.55.0 (`jetstream.ObjectStore`), Benutzer mit Rechten wie in `natsserver.Permissions`, Bucket von einem Administrator vorher angelegt (`<b>` = `viper-<gruppe>`). **Kleinste Menge für `Put` und `Get`:**

| | Menge |
|---|---|
| Veröffentlichen | `$O.<b>.C.*` (Stücke), `$O.<b>.M.*` (Metadaten), `$JS.API.STREAM.INFO.OBJ_<b>` (zum Binden), **`$JS.API.DIRECT.GET.OBJ_<b>.>`** (`Put` sucht damit den vorhandenen Namen; ohne das Recht läuft `Put` und `Get` in ein Zeitlimit), `$JS.API.CONSUMER.CREATE.OBJ_<b>` und `…CREATE.OBJ_<b>.>` (`Get`), `$JS.API.CONSUMER.DELETE.OBJ_<b>.*` (ohne es liefert `Get` die Daten, der Server meldet aber eine Verletzung; der geordnete Consumer hinterlässt nichts) |
| Lesen | `_INBOX.<m>.>` (schon erlaubt); der Client muss **`nats.CustomInboxPrefix("_INBOX.<m>")`** setzen, sonst landen die Antworten in einem fremden `_INBOX.*` |
| Nicht nötig | `$JS.ACK` |
| Verweigert (gemessen, jeweils als Verletzung gemeldet) | `STREAM.PURGE`, `STREAM.UPDATE`, `STREAM.CREATE.<x>`, `STREAM.DELETE`, `STREAM.MSG.DELETE` auf dem Bucket der Gruppe; `ob.Delete` und das Löschen des Buckets; `STREAM.INFO` und `CONSUMER.CREATE` auf dem Bucket **einer anderen Gruppe**, auf KV-Buckets und auf andere Streams; ein Senden an `$O.<anderer Bucket>.M.x`. Subjects mit dem festen Namen des Buckets trennen die Gruppen. |

* **Grenze der Rechte (wichtig):** `Put` auf einen **vorhandenen** Namen gelingt auch ohne `STREAM.PURGE`: Das Löschen der alten Stücke wird verweigert (und von `nats.go` ignoriert), die Metadaten werden überschrieben, die alten Stücke bleiben als Abfall. Und wer `$O.<b>.M.*` und `$O.<b>.C.*` schreiben darf, kann **jedes** Objekt ersetzen oder fälschen oder Stücke zum Füllen des Speichers senden. Die Rechte lassen sich nicht schmaler machen (der Name in `M.*` ist base64 des Namens, ein Präfix je Absender ist nicht erzwingbar). Darum: Integrität durch die **Prüfsumme in der Nachricht**, Obergrenzen am Stream, Restrisiko im Handbuch (§2, §8).
* Der Operator bekommt dieselbe Menge (er sendet über einen SSH-Tunnel, siehe `nats-cli.md`), mit seinem eigenen `_INBOX`-Präfix.
* Alle Maschinen der Gruppe können alle Dateien der Gruppe holen: „in einer Gruppe ist nichts privat“ (`nats.md`).

## 5 Die Nachricht

Neues Feld im Umschlag (`json:"att,omitempty"`):

```json
{"v":1,"kind":"msg","text":"Bitte ansehen","att":{"id":"<32 hex>","type":"pdf","name":"bericht.pdf","size":123456,"sha256":"<64 hex>"}}
```

* `type` ist `pdf`, `zip`, `png`, `jpeg`, `gif` oder `webp`; `name` ist bereinigt (nur Anzeige, höchstens 64 Zeichen, keine Pfadtrenner, keine Steuerzeichen); `id` ist der Objektname; Größe und Prüfsumme begrenzt und geprüft. Ein Anhang gehört zu `msg` und `req`, nicht zu `res`, `presence`, `topics`.
* **Älteres `viper-nats-mcp` lehnt die ganze Nachricht ab** (unbekanntes Feld). Damit der Absender das vorher weiß, melden die neuen Server ihre **Fähigkeiten** auf einem eigenen Subject `viper.<g>.<m>.caps` (wie die Themen auf `….topics`: ältere Server ignorieren ein Subject, das sie nicht abonnieren; ein neues Feld in `presence` täte das nicht, sie lehnen es ab). Inhalt: Version und `["att"]`. `send` an eine Maschine ohne `att` und `broadcast` an ein Thema, das so eine Maschine abonniert hat, **verweigert** den Anhang mit Namen der Maschine; ein Rundruf an unbekannte Empfänger warnt. `viper check` meldet eine Maschine mit alter Version.
* Subject und Themen ändern sich nicht, `maxWire` und `MaxText` bleiben.

## 6 Der MCP-Server

* `send`, `broadcast` und `request` bekommen den Parameter `attachment` (Pfad einer Datei **auf dieser Maschine**, höchstens `NATS_OBJECTS_FILE_MAX`). Der MCP-Server prüft Typ und Größe, legt die Datei im Bucket ab und sendet die Nachricht mit der Referenz. Der Pfad muss unterhalb des Heimverzeichnisses liegen, kein Link auf etwas außerhalb *[?]*.
* **Neues Werkzeug `fetch`** (`id`): holt das Objekt, prüft Größe, Prüfsumme und Typ an den ersten Bytes und legt es unter `<Dir>/attachments/<id>.<endung>` ab (Modus 0600). Es antwortet mit Pfad, Typ und Größe, **nie mit dem Inhalt**. Der Agent liest die Datei danach mit seinen eigenen Mitteln (Claude Code kann Bilder und PDFs aus Dateien lesen).
* `inbox` und der Push nennen einen Anhang als eine Zeile („Anhang: bericht.pdf, pdf, 120 KiB, id …“), nicht mehr. **Es wird nichts automatisch geholt oder geöffnet.**
* Der Ordner `attachments` hat eine Obergrenze (zum Beispiel 64 MiB und 50 Dateien); die ältesten werden gelöscht, wenn eine neue dazukommt.
* `fetch` ist **nicht** in `NATS_MCP_AUTOALLOW=read`: Es schreibt eine Datei. Es gehört zu `all` (und zur Liste der Werkzeuge in `model.MCPTools()` und `viper check`).

## 7 `viper nats`

* `viper nats send|broadcast|request GRUPPE … --pdf DATEI`, `--image DATEI` und `--zip DATEI` (genau eine davon oder keine; der Text darf dazukommen). `viper` prüft Typ und Größe lokal, **bevor** es sich verbindet.
* **Als Maschine** (ohne eigenen Schlüssel): ein **eigener Unterbefehl** `viper-nats-mcp attach-put` (nicht `send`): Die Standardeingabe trägt **nur** die Datei, mit einem kleinen Kopf zuerst (Typ, Größe, SHA-256, eine Zeile) und danach genau diese Bytes; der Text kommt als Argument oder aus `--file` (nie von der Standardeingabe, die heute den Text trägt *[V]*). `viper` liest die Datei lokal nur bis `NATS_OBJECTS_FILE_MAX` und bricht **vor** dem Verbinden ab, wenn sie größer ist. Der Unterbefehl legt die Datei ab und gibt die Referenz als JSON-Zeile aus; `viper` sendet dann die Nachricht wie bisher mit `att`.
* **Als Betreiber:** direkt über den Tunnel mit dem Schlüssel des Betreibers.
* `listen` zeigt eine Nachricht mit Anhang mit der Referenz; **`viper nats fetch GRUPPE ID [--out VERZEICHNIS]`** holt sie auf Ihren Rechner.

## 8 Grenzen und Risiken

* **Lebensdauer:** Dateien leben, solange das Heimverzeichnis der Server-Maschine lebt. `viper down` der Server-Maschine, ein anderer Server oder ein neues Volume löschen sie; ein Empfänger, der später holt, bekommt „nicht gefunden“. Das gehört ins Handbuch.
* **Fremde Inhalte:** Ein Bild oder PDF eines anderen Agenten kann Anweisungen enthalten (Text im Bild, versteckter Text im PDF) und aktive Inhalte (PDF). Kein Filter kann das sicher entfernen. Darum: nichts wird automatisch geholt, geöffnet oder in die Sitzung geschoben; der Typ wird an den Bytes geprüft; das Handbuch sagt es deutlich.
* **ZIP:** Ein Archiv ist ein Weg für ZIP-Bomben und „zip slip“ (Einträge mit `../` oder absoluten Pfaden, die beim Entpacken Dateien außerhalb des Ziels überschreiben), für Verknüpfungen und ausführbare Dateien. `viper` und der MCP-Server **entpacken nie**; sie prüfen nur das Inhaltsverzeichnis (§2) und legen `<id>.zip` ab. Wer entpackt, tut es in ein **leeres** Verzeichnis und mit einem Werkzeug, das Pfade prüft; das Handbuch nennt das, und dass das Archiv fremder Agenten nicht vertrauenswürdig ist. Das Verzeichnis ist fälschbar, die Vorprüfung ersetzt das Entpacken mit Grenzen nicht.
* **Pfad und Namen:** Nichts vom Sender wird zu einem Pfad; Dateiname = `<id>.<endung>` mit fester Endung je Typ.
* **Speicher:** `max_file_store` und TTL schützen den Server. Ein Sender kann den Speicher mit vielen Dateien füllen (die ältesten fallen heraus): Das ist ein Angriff auf andere Anhänge, kein Ausfall des Servers. Das Limit je Datei und die TTL begrenzen es nur.
* **Rechte:** Der Plan nimmt den Bucket als gemeinsam an. Wer in der Gruppe ist, holt alles. Wer Rechte auf `$JS.API.STREAM.PURGE` oder `…DELETE` hätte, leerte ihn; §4 verweigert beides.
* **Zuwachs der Fläche:** JetStream ist eine zusätzliche Angriffsfläche am Server. Er läuft nur mit `NATS_OBJECTS=true`, hinter TLS und Anmeldung wie bisher, mit den Rechten aus §4.
* **Windows, ältere Server:** Der MCP-Server läuft auf den Maschinen (Linux); `viper nats --pdf` liest eine lokale Datei auf jedem Betriebssystem.

## 9 Meilensteine

0. **Prüfen: erledigt (14.11.2026, §4, §11).**
1. **Server:** Einstellungen `NATS_OBJECTS*`, `Render` (`jetstream`-Block), die Unit der VM (`StateDirectory`), `natscheck.sh` (`objects=on|off`), `viper check`. Noch ohne Bucket und Rechte.
2. **Rechte und Bucket:** die Menge aus §4 in `Permissions` und `OperatorPermissions` (nur bei `NATS_OBJECTS=true`), das Anlegen des Buckets durch `viper` mit den Grenzen aus §3, Tests (gegen einen echten Server, auch die Verweigerungen).
3. **Nachricht und MCP-Server:** `att` (Lesen und Prüfen, strenge Grenzen), `caps` (Subject und Prüfung), die Parameter von `send`/`broadcast`/`request`, `fetch`, `inbox`, Obergrenze des Ordners, `MCPTools()`, `viper check`, Tests mit Prüfsumme und Typ an den Bytes, ZIP-Dateien (Bombe, `..`, absoluter Pfad, Verknüpfung, zu viele Einträge, Verzeichnis nicht zur Datei passend), fehlendem Objekt und abgelaufenem Objekt (`object not found`, „digests do not match“ als „abgelaufen oder verändert“).
4. **CLI:** `--pdf`/`--image`, `attach-put`, `viper nats fetch`, Betreiber über den Tunnel.
5. **Handbuch und Läufe:** Handbuch (NATS-Kapitel, Sicherheit, Grenzen, Restrisiko), `developer.md`, ein Lauf mit drei Maschinen gegen echtes Docker und, wenn möglich, gegen k3d.

## 10 Offene Fragen

* Ein Bucket je Gruppe, oder je Absender? Je Absender hülfe nicht: Die Rechte lassen sich nicht auf einen Teil des Buckets beschränken (§4), und viele Buckets bedeuteten viele Streams. Der Plan nimmt **einen** und schützt durch die Prüfsumme.
* Reicht die TTL von 7 Tagen, und reicht ein Anhang je Nachricht?
* Soll `fetch` auch einen Anhang aus einer Gruppe holen dürfen, in der die Maschine nicht Mitglied ist? (Nein: der Bucket ist der der eigenen Gruppe.)
* Soll `viper nats fetch` ohne Schlüssel des Betreibers über eine Maschine laufen (SSH, `fetch`, `scp` zurück)? Der Plan lässt es zunächst nur für den Betreiber zu.

## 11 Messungen (14.11.2026)

Eingebetteter `nats-server` 2.15.2 und `nats.go` 1.55.0, JetStream mit einem `store_dir` im temporären Verzeichnis.

* **Rechte:** siehe §4 (Menge, Verweigerungen, die Verletzungen, die der Server bei zu kleiner Menge meldet: ohne `STREAM.INFO` kein Binden; ohne `DIRECT.GET` ein Zeitlimit bei `Put` und `Get`; ohne `CONSUMER.CREATE` scheitert `Get`; ohne `CONSUMER.DELETE` nur eine Meldung).
* **Überschreiben:** `Put` auf einen vorhandenen Namen gelingt ohne `STREAM.PURGE`; die Metadaten werden ersetzt, die alten Stücke bleiben (3 Nachrichten und 3 Subjects im Stream).
* **Ablauf:** Mit `max_age` allein verfallen Stücke und Metadaten zusammen (`Get` und `GetInfo`: `object not found`). Mit `max_bytes` und `discard: old` fielen zuerst die Stücke: `Get` lieferte „received a corrupt object, digests do not match“, `GetInfo` und `List` zeigten das Objekt weiter an.
* **Voller Speicher:** Der Standard des Object Stores ist `discard: new`; ein Ablegen über `max_bytes` scheitert mit `API error code=503 err_code=10077 maximum bytes exceeded` und lässt Teilstücke zurück. Bei 100 KiB und `discard: new` wurden 582 winzige Objekte (je etwa 175 Byte) angenommen: Viele kleine Objekte füllen den Stream, darum `max_msgs`.
* **Größen:** `max_payload` 1048576, Stückgröße 131072 (1 MB gab 8 Stücke). JetStream kostete im Leerlauf etwa 274 KiB Heap und 16 Goroutinen mehr.
* **Streamflags:** `deny_purge` und `deny_delete` lassen sich nicht setzen, weil die Metadaten Roll-ups brauchen (`err_code=10052`).

# Plan: NATS vom eigenen Rechner ansprechen (`viper nats …`)

**Ziel:** Der Betreiber soll aus der Kommandozeile Nachrichten an die Rechner einer NATS-Gruppe schicken, Anfragen stellen und mitlesen können, ohne eine Claude-Code-Sitzung zu öffnen. Beispiele: ein Rundruf an alle, eine Anfrage an eine Maschine, die Anwesenheit der Gruppe. Dazu gehört ein wählbarer **Absender**.

Grundlage sind [`nats.md`](nats.md) (Gruppe, Rechte, Schlüssel) und [`nats-mcp.md`](nats-mcp.md) (Subjects und Nachrichtenformat des MCP-Servers). Dieser Plan ändert beides nur in Stufe 2 (§5).

**Stand:** Entwurf, einmal unabhängig geprüft; die Befunde sind eingearbeitet (§11). Es ist noch nichts umgesetzt.

**Kennzeichnung:** *[V]* heißt, im Code oder im Plan `nats-mcp.md` bestätigt. *[?]* heißt Vermutung; jede solche Stelle wird in Meilenstein 0 geprüft, bevor etwas darauf gebaut wird. **(zu bestätigen)** markiert Arbeitsannahmen, die eine Entscheidung brauchen.

## 1 Ausgangslage

* `viper` selbst hat keinen NATS-Befehl. Gesendet wird heute nur von den Agenten auf den Rechnern, über die Werkzeuge des MCP-Servers (`peers`, `send`, `broadcast`, `request`, `reply`, `inbox`) *[V]*.
* **Der Absender gehört zur Identität.** Er steht als drittes Token im Subject: `send` und `request` gehen an `viper.<g>.<ich>.out.<empfänger>`, `broadcast` an `viper.<g>.<ich>.pub.<thema>` *[V, `natsmcp/subjects.go`]*. Der NATS-Server erlaubt jedem NKey nur `viper.<g>.<eigener name>.>` und `viper.<g>.*.req.>` zu senden *[V, `natsserver.Permissions`]*. Das gilt für `out`, `pub` und `presence`. Der Teilbaum `viper.<g>.*.req.>` dagegen darf jede Maschine beschreiben; der MCP-Server nimmt ihn nie an *[V, `Names.Parse`]*. Ein anderer Absender ist deshalb nur mit einem anderen NKey möglich; ihn im Inhalt zu behaupten ist wirkungslos, weil der MCP-Server den Absender aus dem Subject liest *[V, `Names.Parse`]*.
* Der Inhalt jeder Nachricht ist ein JSON-Umschlag `{"v":1,"kind":"msg|req|res|presence","id":…,"re":…,"text":…}` mit strenger Prüfung (nur bekannte Schlüssel, `id` hexadezimal 16 bis 64 Zeichen, Grenzen für `text`) *[V, `natsmcp/message.go`]*. Eine Nachricht von außen muss genau dieses Format treffen, sonst wird sie verworfen.
* Der NATS-Server lauscht nur auf der **privaten IP** der Servermaschine *[V, `natsserver.Config`]*. Der Rechner des Betreibers liegt nicht im privaten Netzwerk.
* Auf jedem Rechner liegen `~/.viper-nats/connection.env` (URL, CA-Datei, Seed-Datei, Subject-Präfix) und das Programm `nats` *[V, Handbuch]*.
* `viper ssh --group G --cmd …` führt schon Befehle auf allen Rechnern aus (`sshOnce`) *[V, `group.go`]*.

## 2 Befehle

```
viper nats peers      <gruppe>                                   [--from M]
viper nats send       <gruppe> --to M "text"                     [--from M]
viper nats broadcast  <gruppe> --topic T "text"                  [--from M]
viper nats request    <gruppe> --to M "text" [--timeout S]       [--from M]
viper nats listen     <gruppe> [--topic T] [--from-machine M]    [--from M]
```

* `<gruppe>` ist der Name einer Gruppe aus `groups/<gruppe>.env`. Welcher Rechner als Zugang dient, regelt §3.
* `--from` wählt den **Absender** (Stufe 1: eine Maschine der Gruppe; Stufe 2: zusätzlich der Betreiber-Absender, §5). Ohne `--from` nimmt Stufe 1 den ersten erreichbaren Rechner der Gruppe und Stufe 2 den Betreiber-Absender, sobald er eingerichtet ist (§8).
* Der Text kommt als Argument oder, mit `-`, von der Standardeingabe.
* `request` wartet auf die Antwort-Nachricht (`kind=res`, `re=<id>`) im Bereich des Antwortenden `viper.<g>.<antwortender>.out.<absender>`. **Angenommen wird sie nur, wenn der Absender der Antwort der Befragte (`--to`) ist** und das Subject von `Names.Parse` akzeptiert wird; jede Maschine kann die Anfrage mitlesen und sonst eine Antwort unterschieben *[V, `natsmcp/service.go`: `w.to == in.From`]*. Der Text der Antwort wird ausgegeben. Ohne Antwort endet der Befehl mit Exit-Status 1 nach dem Timeout (Standard 30 Sekunden, höchstens 120, wie im MCP-Server).
* `listen` gibt eingehende Nachrichten fortlaufend aus, bis Strg-C. Es ist nur lesend; mitlesen darf jeder Rechner in `viper.<g>.>` *[V]*. Ausgegeben wird nur, was `Names.Parse` und `Decode` akzeptieren; Zeilen aus dem offenen `req`-Teilbaum erscheinen nicht oder als „ungeprüft“ gekennzeichnet.
* `--to` gleich `--from` ist ein Fehler. Ein `broadcast` erreicht nur Maschinen, die das Thema abonniert haben; die Ausgabe sagt „gesendet, Zustellung nicht bestätigt“.
* Ausgaben an das Terminal laufen durch `natsmcp.Clean` (Steuerzeichen und Bidi-Zeichen), nicht durch `Safe` (das zusätzlich `<`, `>`, `&` maskiert, wie es für Agenten gedacht ist). Das gilt auch für Namen, Themen, `--json` und Fehlertext von der Maschine.
* Exit-Status: 0 ok, 1 Fehler oder keine Antwort, 130 Abbruch. Die Ausgabe ist Text; mit `--json` jeweils eine Zeile je Nachricht (§8, Punkt 4).
* Alle Namen und Themen werden mit `model.CheckNATSName` geprüft, bevor etwas gesendet wird (keine Punkte, Platzhalter, Leerzeichen).

## 3 Stufe 1: ein Rechner der Gruppe als Absender (`--from <maschine>`)

**Idee:** `viper` verbindet sich per SSH mit der gewählten Maschine und sendet dort mit deren Verbindungsdatei. Der Absender ist dann diese Maschine, ihre Identität und ihre Rechte. Am Server und am Schlüsselmaterial ändert sich nichts.

### Ablauf

Die Befehle teilen sich in **Senden** und **Lesen**, weil beide verschieden sicher zu lösen sind.

**Senden (`send`, `broadcast`, Anfrage-Teil von `request`):**

1. Gruppe auflösen (`groupMachines`), Einstellungen der Maschine laden, SSH-Konfiguration prüfen (wie `sshOnce`: ohne `viper up` gibt es keinen Eintrag).
2. Den Umschlag auf dem eigenen Rechner bauen (`natsmcp.Envelope`; `newID`, `publish` und `checkText` sind dort nicht exportiert und werden exportiert oder in einem kleinen Paket bereitgestellt). Die Größe des fertigen JSON wird gegen `maxWire` (32 KiB) geprüft, nicht nur der Text: Steuerzeichen werden beim Kodieren länger.
3. Per SSH `nats pub <subject>` auf der Maschine ausführen, mit `connection.env` als Quelle für URL, CA und Seed. **Der Umschlag geht über die Standardeingabe**, nie als Teil einer Shell-Zeichenkette. Dafür wird `natsgroup.Ops.On` oder `StreamTool` mit Standardeingabe benutzt, **nicht** `sshOnce` (das `-n` setzt und die Standardeingabe schließt, `group.go`). Subject und ID werden vorher streng geprüft; `Ops.On` zitiert Argumente mit `QuoteAll` *[V]*.
4. **Kein automatischer Neuversuch beim Senden.** `Ops.On` wiederholt bei Verbindungsabbruch bis zu 12-mal *[V, `natsgroup.go`]*; bei einem Senden ergäbe das doppelte Nachrichten und eine `req` mit derselben ID mehrfach. Für das Senden wird der Neuversuch abgeschaltet oder `StreamTool` benutzt.
5. Zu prüfen (M0): ob `nats pub` den Rumpf als Vorlage auswertet (`{{…}}`). Wenn ja, wird die Vorlagenauswertung abgeschaltet oder der Rumpf maskiert, sonst kann ein Text Funktionen der Vorlage aufrufen.

**Lesen (`peers`, `listen`, die Antwort von `request`):**

Die Ausgabe von `nats sub` wird **nicht** ausgewertet. Sie enthält die Nutzdaten frei neben der Subject-Zeile; eine Maschine kann in den Nutzdaten eine falsche „Received on …“-Zeile mit zeilenumbruch unterbringen und so einen Absender vortäuschen. Stattdessen:

1. Auf den Maschinen läuft schon `viper-nats-mcp` *[V, wenn `NATS_MCP=true`]*. Es bekommt einen Unterbefehl **`viper-nats-mcp watch`** (und `send`): Er nutzt `Names.Parse` und `Decode`, liest `connection.env` selbst und gibt **eine JSON-Zeile pro Nachricht** aus (`from`, `kind`, `id`, `re`, `text`, bereits gesäubert). `viper` liest nur diese Zeilen.
2. Folge: Die Lese-Befehle der Stufe 1 setzen `NATS_MCP=true` auf der Zugangsmaschine voraus. Ohne das Binary meldet `viper`: „Lesen braucht den MCP-Server auf <maschine>“. Senden mit `nats pub` geht auch ohne.
3. `peers` hört nur zu (Anwesenheit der anderen); es sendet **keine** Anwesenheitsanfrage, denn die würde die Zugangsmaschine bei allen als anwesend eintragen (`Peers.Seen`), obwohl ihr Agent vielleicht nicht läuft. Das Fenster beträgt mindestens 8 Sekunden: Empfänger antworten höchstens alle 5 Sekunden mit bis zu 2 Sekunden Verzögerung *[V, `service.go`]*. Stufe 2 darf die Anfrage als `operator` senden, weil dort kein falscher Maschinenname eingetragen wird.
4. `request`: Der Lese-Teil muss **vor** dem Senden laufen und bestätigen, dass er abonniert hat (eine erste Zeile „bereit“), sonst geht die Antwort verloren. Das Senden folgt erst danach. Zwei SSH-Verbindungen (lesen, senden) oder eine, die beides in dem Unterbefehl erledigt: **(zu bestätigen, M0)**; die zweite ist einfacher und vermeidet das Wettrennen.

### Auswahl des Zugangs

* `--from M`: genau diese Maschine, sonst Fehler.
* Ohne `--from`: die Maschinen der Gruppe in der Reihenfolge der Gruppendatei (deterministisch), bis eine per SSH erreichbar ist und nicht `--to` ist. Die gewählte Maschine steht in der ersten Ausgabezeile („Absender: beta“), damit es nicht verborgen bleibt.

### Was Stufe 1 bewusst nicht kann

* Kein Absender außerhalb der Maschinen der Gruppe.
* Eine Nachricht „von beta“ stammt dann wirklich von beta, ist aber nicht von beta *veranlasst*. Für Empfänger ist das nicht unterscheidbar. Steht beta in `NATS_MCP_PUSH_FROM`, wird der Text des Betreibers bei anderen Agenten **als vertrauenswürdiges Channel-Ereignis in die Sitzung geschoben** und umgeht damit die Push-Liste *[V, `service.go`]*. Deshalb gibt `viper` bei `--from <maschine>` eine **Warnung** aus (und verlangt bei einer Maschine auf der Push-Liste `--yes`). Das gehört auch ins Handbuch; es ist der Hauptgrund für Stufe 2.
* Es setzt eine laufende Maschine mit SSH-Zugang voraus. Ein `peers` über eine Maschine ohne laufende Claude-Sitzung zeigt diese Maschine selbst nicht an (sie sendet keine Anwesenheit), die anderen schon. Das Lesen setzt `NATS_MCP=true` voraus (siehe oben).
* `listen` über SSH hält eine SSH-Verbindung offen; bricht sie ab, endet der Befehl mit Fehler.

### Sicherheit

* Keine neue Berechtigung, keine neue Schlüssel. Der Seed der Maschine bleibt auf der Maschine *[V, `nats.md` §6]*; `viper` liest ihn nie, es führt nur `nats` dort aus.
* Der Betreiber hat über SSH ohnehin Vollzugriff auf die Maschine; Stufe 1 gibt ihm nichts, was er nicht schon könnte.
* Prompt-Injection und Terminal-Escape: Antworttext von Agenten wird auf dem Terminal ausgegeben. Er läuft durch `natsmcp.Clean` *[V, `clean.go`]*, siehe §2.

## 4 Meilensteine, Stufe 1

| Nr. | Meilenstein | Ergebnis |
|---|---|---|
| 0 | Verifikation | **1.** Trifft der Umschlag die Prüfung von `Decode` (lokaler Unit-Test, kein echter Lauf nötig)? **2.** Läuft `nats pub` mit dem Rumpf von der Standardeingabe auf einer Maschine, und wertet es `{{…}}` aus (echter Lauf, Text wie `{{Random 5 5}}`)? **3.** Ein Unterbefehl `watch` im MCP-Binary: Größe, Aufbau, Test. **4.** Entscheidungen aus §8. |
| 1 | Umschlag und Befehle (lokal) | Paket `internal/usecase/natscli`: Namensprüfung, Umschlag, Ausgabe; Port für „führe auf Maschine M aus mit Standardeingabe“ (`StreamTool`, ohne `-n`, ohne Neuversuch beim Senden); `viper-nats-mcp watch/send`. Unit-Tests ohne Netz, darunter ein Fake-`ssh`, dem `'`, `$()`, Zeilenumbruch und `{{…}}` im Text übergeben werden |
| 2 | CLI | `viper nats …`, Hilfe, Fehlermeldungen; Tests mit den Test-Hosts wie `group_test.go` |
| 3 | Integration | Test mit eingebettetem NATS-Server und den Rechten aus `natsserver.AuthorizationBlock`: gesendete Nachricht erscheint beim MCP-Dienst (`natsmcp`) als „von <absender>“; Fälschung scheitert; eine Antwort einer anderen Maschine als `--to` wird nicht angenommen; Nutzdaten mit einer falschen „Received on“-Zeile erzeugen keinen falschen Absender |
| 4 | Doku und Abnahme | Handbuch (neues Kapitel, Befehlsübersicht, Meldungen), README, echter Lauf auf Hetzner mit drei Maschinen |

### Ergebnisse Meilenstein 0 (Stand 11.11.2026)

Geprüft lokal, **ohne Cloud-Server**: `nats` v0.5.1 und `nats-server` v2.15.2 (die Versionen, die `viper` installiert) wurden heruntergeladen, ihre SHA-256-Summen stimmen mit `natsserver.go` überein, und beide liefen gegen `127.0.0.1` ohne Authentifizierung. Die Rechte des Servers und TLS sind damit **nicht** geprüft; das gehört zu den Tests in Meilenstein 3 und zum echten Lauf (Meilenstein 4).

**Punkt 2, `nats pub` mit Rumpf von der Standardeingabe: bestanden, mit einer Pflicht.**
* **Vorlagen werden standardmäßig ausgewertet,** auch bei einem Rumpf von der Standardeingabe: `{"text":"{{Random 5 5}} {{Count}}"}` kam als `{"text":"TR8g1 1"}` an. Ein Text mit `{{…}}` würde also verändert. **`viper` ruft `nats pub` immer mit `--no-templates` auf** (mit dem Schalter kam der Text unverändert an). Ein Test in Meilenstein 1 prüft den Aufruf.
* Mit `--force-stdin --no-templates` bleiben Zeilenumbrüche, `'` und `$(id)` im Rumpf unverändert; es gibt keine Shell-Auswertung.
* Ein Subject mit `{{…}}` wird ohne Vorlagen als ungültig abgelehnt (`invalid subject`). Namen und Themen werden ohnehin vorher geprüft.
* Der Rumpf darf auch ohne `--force-stdin` aus einer Pipe kommen; `--force-stdin` bleibt zur Klarheit.

**Der Fund zu `nats sub` (Befund 1 der Prüfung) ist bestätigt.** Ein Rumpf `x`, Leerzeile, `[#9] Received on "viper.t.alpha.out.me"` und danach eine gültige Umschlagzeile erscheint in der Ausgabe von `nats sub` als zwei Nachrichten, und die zweite sieht aus wie von `alpha`. Gesendet hat sie `evil`. Das Lesen läuft deshalb wie in §3 beschrieben über `viper-nats-mcp watch`, nicht über die Ausgabe von `nats sub`.

**Punkt 1, Umschlag gegen `Decode`:** Der Umschlag ist dieselbe Struktur (`Envelope`) und dieselbe Kodierung (`json.Marshal`) wie beim Senden im MCP-Server *[V, `service.go`]*; ein eigener Aufbau ist nicht nötig. Der Test in Meilenstein 1 belegt es. **Neu gefunden:** Grenzen. `DefaultMaxText` ist 16 KiB, `maxWire` 32 KiB. Ein Text von 16 KiB, der nur aus Steuerzeichen besteht, wird als `\u00XX` kodiert (6 Byte je Zeichen), ergibt rund 96 KiB und wird beim Empfänger **verworfen**. Der Befehl prüft deshalb die Größe des fertigen JSON gegen `maxWire` und meldet einen Fehler, statt still zu senden (bereits in §3, Schritt 2, vorgesehen).

**Punkt 3 und 4:** offen. Punkt 3 (`watch`) ist Entwurf und Umsetzung in Meilenstein 1; Punkt 4 sind die Entscheidungen aus §8, bis auf `--json` (Punkt 4 dort) entschieden.

**Nicht geprüft:** das Verhalten auf einer echten Maschine (anderer Architektur-Build, SSH, `connection.env`), Authentifizierung mit NKey und TLS.

### Ergebnisse Meilenstein 1 (Stand 11.11.2026)

Umgesetzt: `viper-nats-mcp` bekommt die Unterbefehle `watch`, `send`, `broadcast` und `request` (`internal/natsmcp/cli.go`), das Paket `internal/usecase/natscli` (Befehle prüfen, Wörter des Programms bilden, JSON-Zeilen streng lesen, Zugangsmaschine wählen, Ablauf mit einem `Remote`-Port) und `natsserver.MCPBinary`. Die CLI selbst (M2) und der SSH-Adapter für `Remote` fehlen noch.

**Abweichungen vom Plan (bitte prüfen):**
1. **Senden läuft nicht über `nats pub`, sondern über `viper-nats-mcp send|broadcast|request`.** Grund: Die Unterbefehle nutzen dieselben Prüfungen wie die MCP-Werkzeuge (Name, Text, Größe, Antwort nur vom Befragten), und `request` abonniert und sendet in einem Prozess, ohne Wettrennen. Folge: auch das **Senden setzt `NATS_MCP=true`** auf der Zugangsmaschine voraus (§3 sagte: nur Lesen). `nats pub --no-templates` (M0) bleibt als möglicher Ausweg ohne MCP-Binary, ist aber nicht umgesetzt.
2. **`peers` hört standardmäßig 35 Sekunden, nicht 8.** Eine Maschine meldet sich alle 30 Sekunden (`presenceEvery`); ein Fenster von 8 Sekunden sähe ohne Anfrage meist niemanden. Mit `--query` (Anfrage an alle, Fenster 8 Sekunden) geht es schnell, dann gilt die Zugangsmaschine bei den anderen für bis zu 90 Sekunden als anwesend, auch wenn ihr Agent nicht läuft. Der Standard ist die langsame, ehrliche Variante.

**Weitere Funde und Änderungen am vorhandenen Code:**
* `publish` prüft jetzt die Größe des **kodierten** JSON gegen `maxWire` (32 KiB) und meldet einen Fehler (vorher wurde eine Nachricht aus Steuerzeichen gesendet und beim Empfänger still verworfen). Das gilt auch für die MCP-Werkzeuge.
* `logError` schreibt nichts mehr für ein Trennen ohne Fehler (beim bewussten Schließen der Verbindung).
* Die Werkzeuge `send`, `broadcast` und `request` rufen jetzt `SendText`, `BroadcastText` und `Ask` auf; die Ausgabe der Werkzeuge ist unverändert.
* Ein Fehler, den der NATS-Server erst **nach** dem Senden meldet (verweigertes Subject), wird als Fehler des Unterbefehls gemeldet: Der Befehl wartet auf `Flush` und kurz darauf (`settle`).
* `watch` abonniert zusätzlich `viper.<g>.*.pub.>` (alle Themen), liest aber nichts in den Posteingang. Die Themen aus `mcp.env` werden für die Unterbefehle ignoriert; es wird keine Anwesenheit gesendet (außer `--query`).

**Tests (alle mit `-race`):** Unit-Tests für Prüfung der Befehle (Namen mit Punkt, Platzhalter, Leerzeichen, leerer Text, Selbstsendung, Zeitgrenzen), für die Wörter des Programms (der Text steht nie darin), für das strenge Lesen (unbekannte Felder, Ereignisse, Namen und Wege), für den Ablauf mit einem Fake (Standardeingabe unverändert, einmaliger Aufruf, Fehler der Maschine) und für die Wahl der Zugangsmaschine. Integrationstests mit dem NATS-Server als Bibliothek und den echten Rechten: `send`/`broadcast`/`request` als Maschine, Absender aus dem Subject, Text mit Zeilenumbruch, Anführungszeichen, `$(id)` und `{{…}}` unverändert, `watch` zeigt gefälschte „Received on“-Zeilen und JSON-Zeilen im Text als **eine** Nachricht, nicht lesbare und im offenen `req`-Teilbaum liegende Nachrichten erscheinen nicht, die Unterbefehle melden sich nicht in der Gruppe. Der Linter (`golangci-lint`) meldet für die geänderten Pakete nichts.

**Nicht geprüft:** TLS und NKey mit `viper`-eigenen Dateien (Test läuft ohne TLS auf Loopback), der Betrieb über SSH, mehrere gleichzeitige Verbindungen mit demselben Schlüssel gegen einen echten Server (der Test hat zwei: Dienst und Unterbefehl; es läuft). Der Rest der Suite wurde nicht neu gelaufen.

### Ergebnisse Meilenstein 2 und 3 (Stand 11.11.2026)

Umgesetzt: `viper nats peers|listen|send|broadcast|request <gruppe> [text|-]` mit `--to`, `--topic`, `--from`, `--timeout`, `--query`, `--json`, `--yes` (`adapter/driving/cli/nats.go`), der Port `driving.NATSCLI` (die Befehlszeile ruft das Paket `usecase/natscli` nur über ihn auf, wie der Linter es verlangt) und der SSH-Weg (`StreamTool`, ohne `-n` wenn Text da ist, ohne Neuversuch). `watch` beendet sich, wenn die Ausgabe nicht mehr lesbar ist (die SSH-Verbindung brach ab). `--json` ist eingebaut (Entscheidung §8, Punkt 4: ja).

Tests: Unit- und Integrationstests der Befehlszeile (Text über Standardeingabe unverändert, nie in der Befehlszeile; der Absender ist nie der Empfänger; `--from`; Verweigerung ohne `--yes` bei Push-Liste; Optionsprüfung; JSON). Im Paket `natsmcp` zusätzlich: eine Antwort einer anderen Maschine als der gefragten wird nicht angenommen. Die ganze Suite läuft mit `-race` grün, der Linter meldet nichts.

Meilenstein 3 ist damit bis auf den **echten Lauf auf Hetzner** erledigt. Der echte Lauf braucht Server und Kosten und gehört zur Abnahme (Meilenstein 4).

### Ergebnisse Stufe 2 (Stand 11.11.2026)

Umgesetzt: `viper nats init|revoke <gruppe>`, der Betreiber-Eintrag am Server (`natsserver.Client.Operator`, `OperatorPermissions` ohne `req`-Bereiche und ohne `_INBOX`, eigene Registerzeile `# viper-operator`), Schutz des Namens (`WithEntry`, `WithoutClient` lässt den Betreiber stehen, `NATS_OPERATOR_NAME` mit Prüfung gegen den Rechnernamen, `Render` lehnt doppelte Namen ab), das Paket `usecase/natsop` mit dem Port `driving.NATSOperator` (Server finden, eintragen, austragen), der SSH-Tunnel (`ssh -N -L`, `ExitOnForwardFailure`, freier lokaler Port, Host-Hook `OpenTunnel` für Tests) und das Senden aus dem eigenen Prozess (`natsmcp.RunCLI` mit `TLSServerName` = private IP des Servers, Zertifikat bei jedem Aufruf frisch). Die Standardwahl ohne `--from`: Betreiber, wenn der Schlüssel da ist, sonst eine Maschine (§8).

Abweichungen: Die Rechte des Betreibers sind enger als in §5 zuerst beschrieben (keine `req`-Bereiche, kein `_INBOX`; Prüfung Nr. 13). Zum Finden des Servers fragt `viper` die Maschinen der Gruppe der Reihe nach, welche die Konfiguration des Servers hat; es braucht dafür weder Anbieterzugang noch die Cloud-Schnittstelle. `viper check` meldet den Betreiber-Eintrag **nicht**, es nimmt den Namen nur in `NATS_MCP_PUSH_FROM` als gültig an. `down --group` weist nicht auf einen zurückbleibenden lokalen Schlüssel hin.

Tests: Server (Rechte, Register, Namenskonflikte, Erhalt beim Neuschreiben), `natsop` mit einem nachgebildeten Server, die Befehlszeile (Schlüssel einmal erzeugt, mit 0600/0700, `init` ändert bei Fehlern nichts, Name eines Rechners abgelehnt) und ein Test mit einem echten NATS-Server als Bibliothek und den Rechten aus `AuthorizationBlock`: Der Betreiber sendet über `viper nats`, die Maschine sieht `operator` als Absender.

**Nicht geprüft:** der SSH-Tunnel gegen einen echten Server, TLS mit `ServerName` = private IP über den Tunnel (Go prüft bei einer IP die IP-Einträge des Zertifikats *[?]*), das Verhalten bei einem neu angelegten Server (Schlüssel veraltet: der Server kennt ihn nicht, die Meldung wäre dann ein Anmeldefehler), die Rechteprüfung gegen den echten Server.

## 5 Stufe 2: eigener Absender des Betreibers (`--from operator`)

**Idee:** Der Betreiber bekommt eine **eigene Identität** in der Gruppe: einen NKey, der lokal liegt, und einen Eintrag im NATS-Server mit eigenem Namen. Damit sendet `viper` direkt vom eigenen Rechner, ohne Maschine als Zwischenstation, und der Empfänger sieht den echten Absender.

### Entwurf

* **Name:** `operator`, einstellbar mit `NATS_OPERATOR_NAME` (§8). Die Abgrenzung gegen Maschinennamen ist **kein** Eintrag in `model.CheckNATSName`: Diese Funktion dient auch für Gruppen- und Themennamen (`ValidName`) und würde sie mit sperren. Stattdessen: (a) die Prüfung der **Maschinen** (`up`, Gruppendatei) lehnt `operator` ab, (b) `natsRegister` verweigert den Namen, (c) `init` bricht ab, wenn `operator` schon in der Benutzerliste steht (`ParseClients`). Grund: `WithClient` ersetzt den Schlüssel eines vorhandenen Namens *[V, `natsserver.go`]*; ohne (a) bis (c) würde ein Rechner namens `operator` den Schlüssel des Betreibers still übernehmen oder umgekehrt. Ein Test pro Fall.
* **Schlüssel:** Ein NKey-Paar pro Betreiber und Gruppe unter `<state>/nats/<gruppe>/operator.nk` (Modus 0600), erzeugt mit `natsserver.NewIdentity` *[V]*. Der Seed verlässt den Rechner nie; der öffentliche Schlüssel geht an den Server.
* **Rechte (Vorschlag, minimal):** senden nur in `viper.<g>.operator.>`, lesen `viper.<g>.>`. **Ohne** `viper.<g>.*.req.>` und ohne `_INBOX.operator.>`: Der Betreiber braucht beides nicht (Anfragen und Antworten laufen über `…operator.out.<maschine>`), und der `req`-Teilbaum ist der einzige, in dem das Subject den Absender nicht beweist. Dafür bekommt `natsserver` eine eigene Funktion `OperatorPermissions` neben `Permissions`; die Tests für `Permissions` bleiben unverändert.
* **Registrierung:** `operator` ist ein weiterer Eintrag in der Benutzerliste (`natsserver.authorization` wählt für diesen Namen `OperatorPermissions`). `viper nats init <gruppe>` trägt ihn über `natsgroup.Ops.Update` ein. **Zugang zum Server:** `Ops.Peer` braucht ein `model.Machine` und einen `model.Server` aus der Cloud-Schnittstelle *[V, `natsgroup.go`]*; `init` löst deshalb wie `viper down --group` die Maschinen der Gruppe über ihre Einstellungen auf, liest die Servermaschine aus den Labels (`GroupRole`) und braucht die Anmeldung beim Anbieter. Das ist in Meilenstein 5 zu klären, bevor `init` entworfen wird. Der Eintrag muss **ein weiteres `viper up` auf einem Rechner der Gruppe überleben**: `up` schreibt heute die Konfiguration neu *[V, Handbuch]*, und `Update` behält die Einträge der anderen *[V, Kommentar an `natsRegister`]*. Das wird in Meilenstein 6 durch einen Test belegt.
* **Löschen:** `viper down` des letzten Rechners löscht den Server, damit ist der Eintrag weg; der **lokale Schlüssel bleibt dabei zurück**. `viper nats revoke <gruppe>` entfernt Eintrag und lokalen Schlüssel; `down --group` weist am Ende darauf hin, wenn ein lokaler Betreiber-Schlüssel für die Gruppe existiert. Der Ort ist der Zustandsordner von `viper` (`settingsfile/config.go`), nicht ein neuer.
* **Verbindung:** Der Server lauscht nur auf der privaten IP *[V]*. `viper` baut einen **SSH-Tunnel** (`ssh -N -o ExitOnForwardFailure=yes -L 127.0.0.1:<freier port>:<privateIP>:<NATS_PORT>`; der Port ist `NATS_PORT`, nicht fest 4222) über die Servermaschine oder eine erreichbare Maschine der Gruppe auf und verbindet sich lokal. Das Zertifikat gilt für `subjectAltName=IP:<privateIP>` *[V, `natscert.sh`]*. Die Verbindung nutzt `nats.Secure(&tls.Config{ServerName: "<privateIP>", RootCAs: …})`; Go prüft bei einer IP als `ServerName` die IP-Einträge *[?, in M5 belegen]*. **Die CA wird bei jeder Verbindung frisch von der Maschine geholt** und nicht dauerhaft gespeichert: Das Zertifikat läuft ab und wird nicht erneuert, und eine neu angelegte Gruppe hat eine neue IP und eine neue CA. Ein veralteter lokaler Schlüssel (Gruppe neu angelegt, Eintrag fehlt im Server) wird erkannt und mit klarer Meldung („`viper nats init` erneut“) gemeldet, nicht mit einem TLS-Fehler.
* **Client:** Go mit `nats.go` und `nkeys` direkt in `viper`; beide sind schon direkte Abhängigkeiten des Hauptmoduls *[V, `go.mod`]*. Kein Aufruf von `nats` auf einer Maschine nötig. Dieselben Regeln wie in Stufe 1: Antworten nur vom Befragten, nur Subjects, die `Names.Parse` akzeptiert.

### Neue Befehle und Optionen

```
viper nats init    <gruppe>      # Schlüssel erzeugen, im Server eintragen
viper nats revoke  <gruppe>      # Eintrag entfernen, Schlüssel löschen
viper nats …       --from operator
```

`--from operator` ist ab dann der Standard. `--from <maschine>` bleibt (Stufe 1), `--from` zusammen mit einem unbekannten Namen ist ein Fehler.

### Fragen der Sicherheit

* **Neue Berechtigung.** Stufe 2 gibt dem Rechner des Betreibers einen Zugang zum NATS-Server. Der Seed liegt nur dort; wer ihn hat, kann als `operator` senden und alles in der Gruppe mitlesen. Das ist dieselbe Vertraulichkeit wie bei jeder Maschine *[V, §7 von `nats-mcp.md`]*.
* **Täuschung der Agenten.** `operator` ist für Agenten ein neuer Absender, der Anweisungen schickt. Das ist gewollt, aber die Agenten behandeln Nachrichten sonst als Aussagen eines Kollegen. Das Handbuch sagt klar, dass `operator` der Mensch ist. `NATS_MCP_PUSH_FROM` wird gegen Rechnernamen der Gruppe geprüft *[V, `check`]*; für `operator` braucht es eine ausdrückliche Regel (§8, Punkt 3: optional erlaubt).
* **Eine bestehende Gruppe ohne `init`** ändert sich nicht. Der `operator`-Eintrag entsteht nur auf Befehl.
* **`natsserver.Permissions` und die Regeln aus `nats.md` §6** bleiben unverändert, weil `operator` nur ein weiterer Benutzer mit demselben Schema ist. Die Tests, die „genau diese Rechte“ prüfen, bekommen einen Fall dazu.

## 6 Meilensteine, Stufe 2

| Nr. | Meilenstein | Ergebnis |
|---|---|---|
| 5 | Verifikation | **1.** TLS über den Tunnel mit `ServerName` = private IP (echter Lauf), **2.** der Eintrag überlebt ein weiteres `viper up` und ein Neuschreiben der Konfiguration, **3.** Zugang von `init` zu Anbieter und Server (§5), **4.** Tunnel-Optionen und ob `sshd` Weiterleitung erlaubt. |
| 6 | Schlüssel und Registrierung | `viper nats init/revoke`, reservierter Name (drei Prüfungen, §5), `OperatorPermissions`, Tests mit `natsgroup` (Update behält den Eintrag; ein Rechner namens `operator` wird abgelehnt) |
| 7 | Verbindung | SSH-Tunnel, Client, die Befehle aus §2 mit `--from operator` |
| 8 | `check` | `viper check` meldet einen Betreiber-Eintrag, der nicht (mehr) zum Server passt, und einen fehlenden oder veralteten lokalen Schlüssel; `down --group` weist auf einen übrig bleibenden Schlüssel hin |
| 9 | Doku und Abnahme | Handbuch, Maintainer-Doku, echter Lauf mit drei Maschinen |

## 7 Teststrategie

* **Unit:** Namensprüfung (Punkte, `*`, `>`, Leerzeichen, Großschreibung, Bidi-Zeichen, `operator` als Maschinenname); Umschlag (alle Grenzen, Text mit Zeilenumbruch und JSON-Sonderzeichen, Text aus der Standardeingabe); Auswahl des Zugangs (erste erreichbare Maschine, `--from` unbekannt); Ausgabe ohne Steuerzeichen.
* **Integration:** NATS-Server als Bibliothek mit `natsserver.AuthorizationBlock` und den echten Rechten (Loopback, ohne TLS). A: `viper nats send` erscheint beim MCP-Dienst von B als „von A“. A sendet auf `viper.<g>.b.out.c`: abgelehnt (asynchroner Fehler geprüft). Eine Antwort von C auf eine Anfrage an B wird nicht angenommen. Stufe 2: `operator` wie A; `operator` kann nicht in `viper.<g>.alpha.>` und nicht in `viper.<g>.*.req.>` senden.
* **Echt (Hetzner, Testprojekt):** drei Maschinen; Stufe 1 mit `--from` auf jeder; Stufe 2 mit Tunnel; ein weiteres `viper up` auf einer Maschine; `viper nats revoke`.
* **Regression:** Alle bestehenden Tests; die Konfiguration einer Gruppe ohne `init` bleibt byte-identisch (Referenzdatei).

## 8 Entscheidungen

Entschieden am 11.11.2026:

1. **`--from` ist optional und hat einen Standard:** Stufe 1: die erste erreichbare Maschine der Gruppe (Reihenfolge der Gruppendatei, nie `--to`); ab Stufe 2: `operator`, wenn für die Gruppe eingerichtet. Die gewählte Quelle steht in der ersten Ausgabezeile.
2. **Der Name des Betreibers ist einstellbar** (`NATS_OPERATOR_NAME`, Standard `operator`) *(Auslegung der Antwort „ja“ auf „fest oder einstellbar“; bitte bestätigen)*. Die Prüfungen aus §5 gelten für den eingestellten Namen.
3. **`operator` in `NATS_MCP_PUSH_FROM` ist optional einstellbar:** erlaubt, wenn der Betreiber es einträgt; Standard bleibt die leere Liste. `check` akzeptiert den Betreiber-Namen dort.
4. **`--json`:** Vorschlag **ja**, weil es billig ist: eine Zeile pro Nachricht, derselbe Inhalt wie der Text (`from`, `kind`, `id`, `re`, `text`), bereits gesäubert. Es erleichtert Skripte und Tests und braucht keine eigene Logik, da `watch` ohnehin JSON-Zeilen liefert. Noch offen, bis du zustimmst.
5. **Reihenfolge:** erst Stufe 1 (Meilensteine 0 bis 4), danach Stufe 2 (5 bis 9). Stufe 2 beginnt erst, wenn Stufe 1 abgenommen ist.

## 9 Bewusst nicht enthalten

* Beliebige Subjects oder NATS-Befehle (wie bei den Agenten *[V, `nats-mcp.md` §7]*).
* JetStream, Speicherung, Wiederholung verpasster Nachrichten.
* Mehrere Betreiber pro Gruppe, mehrere Gruppen in einem Befehl.
* Signaturen oder Ende-zu-Ende-Verschlüsselung.
* Ein Absender, der keine eigene Identität im Server hat (nicht möglich, §1).

## 10 Definition of Done

1. Stufe 1: `viper nats peers|send|broadcast|request|listen --from <maschine>` funktioniert gegen eine echte Gruppe; der Empfänger sieht den gewählten Absender.
2. Stufe 2: Mit `viper nats init` sendet `viper` als `operator` ohne Maschine als Zwischenstation; ein weiteres `viper up` entfernt den Eintrag nicht; `revoke` entfernt ihn.
3. Fälschungsversuche (anderer Absender, fremdes Subject) scheitern am Server, belegt mit eingebettetem Server und echtem Lauf.
4. Ohne Aufruf von `viper nats` und ohne `init` ändert sich für bestehende Gruppen nichts.
5. Keine neue Berechtigung für Maschinen; die Berechtigung des Betreibers ist im Handbuch beschrieben (Vertraulichkeit, Seed, Täuschung der Agenten).
6. Handbuch, README und Maintainer-Doku sind nachgeführt.

## 11 Prüfung (Stand 11.11.2026)

Der Plan wurde einmal von einem unabhängigen Prüfer gegen den Code gelesen (nichts ausgeführt). Eingearbeitet:

| Befund | Änderung |
|---|---|
| `nats sub`-Ausgabe auszuwerten erlaubt einen vorgetäuschten Absender | Lesen über `viper-nats-mcp watch` mit `Parse` und `Decode`, eine JSON-Zeile pro Nachricht (§3) |
| `request` nahm Antworten von jedem an | Nur vom Befragten und nur über `Names.Parse` (§2); Test in M3 |
| `operator` als reservierter Name: falscher Ort, stiller Schlüsselaustausch | Drei Prüfungen statt `CheckNATSName` (§5); Test pro Fall |
| Text zu `nats pub` unklar, `-n`, Neuversuche der Funktion `On` | Standardeingabe, `StreamTool`, kein Neuversuch beim Senden, Fake-`ssh`-Test (§3, M1) |
| `peers` sendete eine Anwesenheitsanfrage als Maschine | Stufe 1 hört nur zu, Fenster mindestens 8 Sekunden (§3) |
| Umgehung der Push-Liste durch `--from <maschine>` | Warnung und `--yes` (§3) |
| Teilbaum `req` beweist den Absender nicht | §1, §2 (`listen` filtert) |
| Zugang von `init`, Zertifikat zwischengespeichert, veralteter Schlüssel | Zugang geklärt in M5; CA frisch holen; Erkennung (§5) |
| Tunnel: Port, `ExitOnForwardFailure` | `NATS_PORT`, Optionen (§5) |
| Meilensteinverweise, Rechte des Betreibers, `Clean` statt `Safe`, `--to` gleich `--from`, Handbuchbeispiel `res.<anfragender>` | §2, §5, §6; das Beispiel im Handbuch (Zeile 411) ist zu berichtigen |

Nicht geprüft: das Verhalten von `nats pub`/`nats sub` (Vorlagen, Ausgabeformat), die `sshd`-Einstellungen für Weiterleitung, TLS in der Praxis, die Architekturregeln des Linters. Das sind die Punkte der Meilensteine 0 und 5.

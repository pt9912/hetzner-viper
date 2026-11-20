# Plan: Eine lokale Claude-Sitzung in der NATS-Gruppe (`viper nats mcp`)

**Ziel:** Eine Claude-Code-Sitzung auf dem **eigenen Rechner** (Laptop, Entwicklungsumgebung) soll mit den Sitzungen auf den Rechnern einer NATS-Gruppe reden können: wer ist da, Nachricht senden, fragen und die Antwort bekommen, auf Fragen der Rechner antworten, Rundrufe, Dateien. Dafür soll der vorhandene MCP-Dienst (`viper-nats-mcp`) **im Programm `viper`** laufen, mit dem Tunnel und dem Schlüssel, die `viper nats` und der Chat schon benutzen. Auf den Rechnern ändert sich nichts.

**Stand:** Entwurf (20.11.2026). Gebaut ist nichts. Die Entscheidungen in §8 sind **offen**, zu jeder gibt es einen Vorschlag. *[V]* heißt: im Code gelesen; *[?]* ist eine Vermutung, die in Meilenstein 0 zu prüfen ist.

Gehört zu [`nats-mcp.md`](nats-mcp.md) (der Dienst und seine Werkzeuge), [`nats-cli.md`](nats-cli.md) (`viper nats`, der Operator) und [`ui-chat.md`](ui-chat.md) (Tunnel und Strom vom eigenen Rechner). Hängt mit [`a2a.md`](a2a.md) über die **Identitäten mit Namen** zusammen (§3): beide Pläne brauchen sie, sie werden nur einmal gebaut.

## 1 Ausgangslage

* **Was es gibt** *[V]*: `viper-nats-mcp` ist ein MCP-Server über stdio (`natsmcp.Run(ctx, cfg, version, stdin, stdout, log)`). Er liest seine Anmeldung aus `connection.env` und `mcp.env` (Adresse `NATS_URL`, Schlüsseldatei, Zertifikat, Gruppe und Name über `VIPER_NATS_SUBJECT_PREFIX`) und bietet die Werkzeuge `peers`, `topics`, `send`, `broadcast`, `request`, `reply`, `inbox`, `subscribe`, `unsubscribe`. Nichts im Dienst ist an einen Server oder Linux gebunden außer der Verteilung.
* **Der Operator** *[V]*: `viper nats init` trägt einen Eintrag mit eigenem Schlüssel (`groups/<gruppe>/operator.nk`) im Server ein. Er darf nur im eigenen Teilbaum `viper.<g>.<name>.>` senden und liest die ganze Gruppe (`OperatorPermissions`); eine Antwort ist eine gewöhnliche Nachricht an ihn. `viper nats` und der Chat erreichen den Server über einen **ssh-Tunnel** (`openOperatorDead`), den sie selbst öffnen und schließen. Server und `natsop` tragen **mehrere** Einträge mit Namen je Gruppe (Namen nur nicht doppelt, `checkedClients`, `Register(name)`).
* **Was für den Laptop fehlt** *[V]*:
  1. **Der Tunnel:** der MCP-Dienst öffnet keinen.
  2. **Das Zertifikat:** es gilt für die private Adresse des Servers; am Tunnel steht `127.0.0.1`. `Config.TLSServerName` gibt es, `LoadConfig` liest es **nicht** aus `connection.env`.
  3. **Das Programm:** `viper mcp-binary` schreibt nur Linux-Dateien, die Releases enthalten `viper`, aber nicht `viper-nats-mcp`; der MCP-Modus steckt nicht in `viper`.
  4. **Die Dateien:** `viper nats init` legt nur `operator.nk` an, kein `connection.env` für den Laptop.
* **Was heute schon geht:** Die lokale Sitzung ruft über ihr Bash-Werkzeug `viper nats send|request|reply|listen` auf. Das sind Einzelaufrufe ohne Posteingang, der von selbst anspringt.

## 2 Grundregeln

1. **Ein Programm, ein Tunnel, ein Schlüssel:** `viper nats mcp GRUPPE` öffnet den Tunnel selbst und startet im selben Prozess den vorhandenen MCP-Dienst. Nichts wird neu erfunden: `natsmcp.Run`, `openOperatorDead`, `loadNatsGroup`.
2. **Eine eigene Identität für die lokale Sitzung**, nicht der Operator (§3). Wer sie in `NATS_MCP_PUSH_FROM` eines Rechners einträgt, entscheidet das ausdrücklich.
3. **Nichts wird in die lokale Sitzung geschoben**, solange der Betreiber es nicht einschaltet (Pull ist der Standard). Die lokale Sitzung hat meist mehr Rechte (Dateien, Zugangsdaten, ssh-Schlüssel) als die Sitzung eines Rechners; Text von dort ist Text eines Fremden.
4. **Keine Datei der lokalen Platte geht in die Gruppe**, außer aus einem Ordner, den der Betreiber nennt (§5.3).
5. **Auf den Rechnern ändert sich nichts**, außer dass ein Betreiber den neuen Namen in `NATS_MCP_PUSH_FROM` aufnehmen kann.
6. **`stdout` gehört dem Protokoll.** Alles, was `viper` sonst ausgibt (Hinweise, Notizen), geht auf `stderr` oder in eine Datei.

## 3 Identitäten mit Namen (gemeinsam mit dem A2A-Plan)

* Neu: `viper nats init GRUPPE --name NAME` trägt einen **weiteren** Eintrag der Art „Operator“ (gleiche Rechte, eigener Name) ein. Der Schlüssel liegt in `groups/<gruppe>/<name>.nk` (`0600`); ohne `--name` bleibt alles wie heute (`operator`, `operator.nk`). `viper nats revoke GRUPPE --name NAME` entfernt ihn.
* `NAME` ist `CheckNATSName`-gültig, nicht der Name eines Rechners der Gruppe und nicht `NATS_OPERATOR_NAME` (der Server lehnt gleiche Namen ab; `settingsfile` prüft es vorher).
* **Zu ändern** *[V]*: `natsGroup.operator` ist **ein** Name; `model.OperatorKeyFile` und `keyFile()` kennen nur `operator.nk`; `init` und `revoke` bekommen `--name`; `send`, `request` und die anderen nehmen den Namen über das vorhandene `--from` (heute der Name eines Rechners, künftig auch der einer solchen Identität). `natsserver` und `natsop` brauchen nur einen Test.
* **Der Name `a2a`** (A2A-Plan) ist ein solcher Name. Beide Pläne teilen diesen Baustein.

## 4 `viper nats mcp`

```
viper nats mcp GRUPPE [--name NAME] [--push-from m1,m2] [--files ORDNER] [--yes]
```

* Liest den Schlüssel des Namens (Standard `local`, §8.1), öffnet den Tunnel wie `openOperatorDead` und baut die `natsmcp.Config`: `URL` = Adresse am Tunnel, `TLSServerName` = der Name im Zertifikat des Servers, `CAFile` = das Zertifikat des Servers in einer temporären Datei, `Names{Group, Me}`, `ObjectsFileMax`, `PushFrom` (leer), `LogFile` unter `<Zustandsverzeichnis>/nats-mcp/<gruppe>-<name>.log`. Dann `natsmcp.Run(ctx, cfg, version, os.Stdin, os.Stdout, log)`.
* **Eintragen in Claude Code:** `claude mcp add viper-nats -- viper nats mcp GRUPPE` (Handbuch). Mehrere Gruppen sind mehrere Einträge. `viper nats mcp GRUPPE --print-config` gibt den Befehl und die JSON-Form aus.
* **Fehler vor dem Start** (kein Schlüssel für den Namen, Server nicht erreichbar, Gruppe nicht gestartet) gehen mit einem klaren Satz auf `stderr` und Rückgabewert 1; Claude Code zeigt sie bei `/mcp`.
* **Beenden:** Claude Code schließt `stdin`; der Dienst meldet sich ab (`bye`) und der Tunnel wird geschlossen.

### 4.1 Der Tunnel stirbt

Der Tunnel ist ein `ssh -N -L` (Wächter `dead`, wie im Chat). Stirbt er, würde der Dienst auf einen Port ohne Gegenstelle zeigen. Zwei Stufen:

* **Stufe 1 (Meilenstein 2):** Der Prozess endet mit einer Meldung („Tunnel zum Server beendet; `/mcp` verbindet neu“). Claude Code startet einen stdio-Server nicht von selbst neu; die Sitzung sieht die Werkzeuge verschwinden.
* **Stufe 2 (Meilenstein 3):** Der Prozess **bleibt**, wählt **einmal** einen freien lokalen Port und startet den Tunnel auf **demselben Port** neu; der NATS-Client verbindet sich selbst wieder (`nats.go` baut die Verbindung neu auf). *[?: ob `sshTunnel` einen gewünschten Port nimmt und ob der NATS-Client nach dem Neustart des Tunnels sauber weitermacht, wird in Meilenstein 0 mit einem echten `sshd` gemessen (wie in `ui-chat.md` §7.1: Ein Tunnel, dessen Server hängt, endet nach `Interval×(Count+1)`, etwa 75 Sekunden).]*

## 5 Sicherheitsmodell

### 5.1 Was neu ist

Die lokale Sitzung liest Nachrichten von Agenten, die nicht dir gehören müssen (jede Maschine der Gruppe schreibt), und kann in deren Sitzungen schreiben. Beides ist heute so, wenn du `viper nats` aufrufst; neu ist, dass **ein Agent** es tut, ohne dass du jede Nachricht siehst.

### 5.2 Richtung Gruppe → lokale Sitzung

* Eingehender Text kommt in `inbox` und in die Ausgabe von `request`, bereinigt (`natsmcp.Clean`), und die `instructions` des Dienstes sagen, dass es Nachrichten anderer Agenten sind. **Das verhindert Prompt-Injektion nicht** (`nats-mcp.md` §3).
* **Push in die lokale Sitzung** gibt es nur mit `--push-from NAME,…` **und** dem Channel-Schalter von Claude Code (`nats-mcp.md` §3, Betriebsmodell); Standard ist leer, also nie. Ein Rechner, dessen Agent übernommen ist, kann sonst Text direkt in die Sitzung deines Laptops schreiben.
* Antworten auf eine Frage (`request`) werden nur von dem angenommen, den man fragte (vorhandene Regel).

### 5.3 Richtung lokale Sitzung → Gruppe

* Der Absender ist der Name der lokalen Sitzung (aus dem Subject, vom Server erzwungen).
* **Rückfrage beim Start:** Führt ein Rechner der Gruppe den Namen in `NATS_MCP_PUSH_FROM`, landet Text der lokalen Sitzung **ohne Rückfrage** in dessen Sitzung. `viper nats mcp` nennt diese Rechner beim Start auf `stderr` und verlangt für diesen Fall `--yes` (wie der Chat bei `NATS_MCP_PUSH_FROM`). Ohne `--yes` startet es nicht.
* **Dateien:** Das Werkzeug nimmt für `send`, `broadcast` und `request` einen Pfad (`attachment`). Auf einem Rechner ist das die Sache des Rechners; auf dem Laptop könnte ein eingeschleuster Auftrag so `~/.ssh/id_…` in die Gruppe schicken. Darum: **ohne `--files ORDNER` gibt es keine Anhänge** (der Pfad wird abgelehnt), mit `--files` nur Dateien **unterhalb** dieses Ordners (nach dem Auflösen von `..` und Verknüpfungen) und nur die Arten, die auch die Rechner annehmen.
* **Die Gruppe ist offen:** Jede Maschine der Gruppe liest, was die lokale Sitzung sendet (`nats-mcp.md` §3). Nichts Vertrauliches senden. Das gilt auch umgekehrt für das, was die Sitzung hört.

### 5.4 Schlüssel und Protokoll

* Der Schlüssel liegt mit `0600` im Zustandsverzeichnis, neben `operator.nk`. Ein Programm, das den Rechner liest, kann ihn benutzen; das ist bei `operator.nk` heute so. `viper nats revoke --name` löscht ihn und trägt den Namen aus dem Server aus.
* Das Protokoll (`mcp.log`) enthält keine Texte der Nachrichten, nur Absender, Art und Größe (wie auf den Rechnern, `nats-mcp.md` §4.1).

## 6 Umsetzung

| Stelle | Änderung |
|---|---|
| `internal/model` | Schlüsseldatei je Name (`OperatorKeyFile(state, group, name)`); Name `local` als Standard der lokalen Sitzung |
| `internal/adapter/driven/settingsfile` | die Prüfung des Operatornamens gilt für jeden Namen (kein Rechnername); `NATS_OPERATOR_NAME` unverändert |
| `internal/adapter/driving/cli` | `viper nats init|revoke --name`, `viper nats mcp`, `--print-config`; `natsGroup` kennt den gewählten Namen; `openOperatorDead` nimmt Name und (Stufe 2) einen Port; Meldungen nur auf `stderr` |
| `internal/natsmcp` | `Config` wird aus dem Programm gebaut (sie ist ein Wert, `LoadConfig` bleibt für die Rechner); **Anhänge nur unterhalb eines Ordners** (`Config.AttachRoot`) |
| Handbuch, README, Hilfe | Kapitel „Mit einer lokalen Claude-Sitzung in der Gruppe sprechen“ (Ist-Zustand), Befehlstabelle |

## 7 Meilensteine

| | Inhalt | Prüfung |
|---|---|---|
| 0 | **Verifikation:** ein zweiter Eintrag der Art „Operator“ im Server (Test mit eingebettetem Server); der MCP-Dienst mit Operator-Schlüssel und Tunnel-Adresse samt `TLSServerName` verbindet und sendet; `stdout` bleibt rein (kein Byte außer dem Protokoll); Neustart des Tunnels auf demselben Port mit einem echten `sshd` (Stufe 2); Claude Code startet den Eintrag und zeigt die Werkzeuge (**von dir von Hand zu prüfen**, ich kann Claude Code nicht starten) | Messungen im Plan, Entscheidung zu §8.3 |
| 1 | **Identitäten mit Namen:** `init|revoke --name`, Schlüsseldatei je Name, Prüfungen, Hilfe; das vorhandene `--from` nimmt den Namen | Go-Tests (Namen, Schlüssel, zwei Einträge), Test mit eingebettetem Server |
| 2 | **`viper nats mcp` (Stufe 1):** Tunnel, `Config`, Start, Ende, `--yes` bei `PUSH_FROM`, `--print-config`, Protokolldatei | Test mit eingebettetem Server: MCP über Rohre (`tools/call send`, `request` mit einem Rechner als Dienst, `inbox`), `stdout`-Prüfung, Test der Rückfrage |
| 3 | **Tunnel stirbt (Stufe 2):** gleicher Port, Neuverbindung | Test mit einem echten `sshd` (wie in Meilenstein 0 von `ui-chat.md`) |
| 4 | **Dateien:** `--files`, Anhänge nur unterhalb des Ordners | Tests der Grenzen (`..`, Verknüpfungen, fremde Art, ohne `--files`) |
| 5 | **Handbuch, README, Hilfe**; Plan-Stand | Durchsicht, unabhängige Prüfung |

Jeder Meilenstein endet mit grüner CI auf Linux, macOS und Windows (der Tunnel als Kindprozess ist dort die Stelle, die zu prüfen ist) und einer unabhängigen Prüfung.

## 8 Entscheidungen (offen, mit Vorschlägen)

1. **Name der lokalen Sitzung:**
   * **A (Vorschlag):** eigener Name, Standard `local`, mit `--name` änderbar. Der Operator-Name bleibt dem Menschen (Chat, `viper nats`) vorbehalten. Wer die Sitzung in `PUSH_FROM` aufnehmen will, nennt `local`.
   * B: der Operator-Name. Weniger zu bauen, aber ein Agent erbte das Vertrauen aus `NATS_MCP_PUSH_FROM=operator`.
   * C: ein Name je Sitzung (zufällig). Verlangte eine Anmeldung am Server **je Start** (ssh), also unbrauchbar.
2. **Push in die lokale Sitzung:** **Vorschlag: aus** (Pull), einschaltbar mit `--push-from`. Gegenargument: Ohne Push erfährt der Agent von einer Antwort nur, wenn er `inbox` abfragt; `request` wartet aber selbst und gibt die Antwort zurück.
3. **Tunnel stirbt:** **Vorschlag: zuerst Beenden mit klarer Meldung (Stufe 1), später Neuverbinden (Stufe 2)**, wenn die Messung in Meilenstein 0 zeigt, dass es einfach ist (gleicher Port).
4. **Anhänge:** **Vorschlag: aus, nur mit `--files ORDNER`.** Gegenargument: bequemer, aber auf einem Laptop ein offenes Tor für ausgelesene Dateien.
5. **Eine Gruppe je Eintrag:** **Vorschlag: ja.** Mehrere Gruppen sind mehrere Einträge in Claude Code; der Dienst hat eine Verbindung und einen Namen.
6. **Mehrere lokale Sitzungen:** Alle Sitzungen mit demselben Namen teilen die Identität; jede Sitzung bekommt eine eigene Kopie der Nachrichten an den Namen, und eine Antwort an `local` kommt bei **allen** an. **Vorschlag:** das Handbuch sagt es; wer getrennte Gespräche braucht, nimmt `--name` je Sitzung (`viper nats init GRUPPE --name local-b`).
7. **Hilfsbefehl `--print-config`:** **Vorschlag: ja** (der Befehl `claude mcp add …` und die JSON-Form), damit niemand Pfade und Namen von Hand zusammensetzt.

## 9 Bewusst nicht enthalten

* **Der A2A-Weg** ([`a2a.md`](a2a.md)): ein anderer Zweck (fremde Agenten von außen).
* **Mehrere Gruppen in einem Prozess**, eine Oberfläche, JetStream.
* **Push als Standard**, `NATS_MCP_AUTOALLOW` für die lokale Sitzung (Claude Code fragt bei jedem Werkzeug; das ist hier der Schutz).
* **Neue Rechte auf den Rechnern** und Änderungen an `viper-nats-mcp` auf den Rechnern.

## 10 Risiken

| Risiko | Gegenmaßnahme |
|---|---|
| Ein übernommener Agent schreibt in die Sitzung des Laptops | Pull als Standard, Push nur mit Schalter, Text als fremd gekennzeichnet |
| Die lokale Sitzung schiebt Text in Sitzungen der Rechner (`PUSH_FROM`) | eigener Name, Rückfrage beim Start (`--yes`), das Handbuch sagt es |
| Eingeschleuste Aufträge schicken lokale Dateien in die Gruppe | Anhänge nur mit `--files` und nur unterhalb des Ordners |
| `stdout` wird mit Meldungen verunreinigt, Claude Code bricht ab | Test auf reines Protokoll, Meldungen nur auf `stderr` |
| Tunnel stirbt unbemerkt | Wächter, klare Meldung (Stufe 1), Neuverbindung (Stufe 2) |
| Ein Name, mehrere Sitzungen: Antworten kommen an der falschen an | Handbuch, `--name` je Sitzung |
| Schlüssel auf dem Laptop gestohlen | `0600`, `viper nats revoke --name`; wie `operator.nk` heute |

## 11 Definition of Done

* `viper nats init GRUPPE --name local` und `viper nats mcp GRUPPE` laufen auf Linux, macOS und Windows; `claude mcp add viper-nats -- viper nats mcp GRUPPE` zeigt die Werkzeuge.
* Eine lokale Sitzung sieht mit `peers` die Rechner, sendet mit `send`, fragt mit `request` und bekommt die Antwort, beantwortet die Frage eines Rechners mit `reply` und findet Eingehendes in `inbox`.
* Ohne `--push-from` wird nichts in die lokale Sitzung geschoben; ohne `--files` geht keine Datei hinaus; mit einem Rechner, der den Namen in `PUSH_FROM` führt, startet der Befehl nur mit `--yes`.
* `stdout` enthält nur Protokoll (getestet).
* Auf den Rechnern ändert sich nichts.
* Tests: Go-Tests (Namen, Schlüssel, Grenzen der Anhänge), Tests mit eingebettetem NATS-Server (MCP über Rohre, ein Rechner als Dienst), ein Test des Tunnels mit echtem `sshd`; Lint und Tests grün in der CI auf drei Systemen.
* Handbuch (Ist-Zustand), README und Hilfe stimmen; **jeder Meilenstein unabhängig geprüft**.

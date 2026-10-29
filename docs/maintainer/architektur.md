# Architektur von viper

`viper` ist nach dem Muster Ports und Adapter (hexagonale Architektur) gebaut. Die Regeln dafür prüft der Linter (`make lint`, Abschnitt `depguard` in `.golangci.yml`); sie sind keine Empfehlung.

## Schichten

```
cmd/viper/main.go          ruft nur app.Run auf
internal/
  model/                   Server, ServerSpec, Machine, Finding, Anbietertabelle (importiert nichts aus viper)
  port/
    driving/               Up, Down, Check, Preflight: was die CLI vom Programm verlangt
    driven/                cloud, dns, system, runlog, sshconfig, settings: was das Programm von außen braucht
  usecase/                 start (Up), stop (Down), check, preflight: die Abläufe
  adapter/
    driving/cli            Befehle, Parsen der Befehlszeile
    driven/                hetzner, aws, gcp, azure, cloudflare, nodns, system, editor, git, resolver,
                           runlog, sshconfig, settingsfile
  app/                     Composition Root: wählt die Adapter, baut die Use Cases, übergibt sie der CLI
```

Die Abhängigkeiten laufen nach innen: `model` kennt nichts, die Ports kennen nur das Modell, die Use Cases kennen Ports und Modell, die Adapter erfüllen Ports (driven) oder rufen sie auf (driving). Nur `app` kennt alles und steckt es zusammen. Unterschiede zwischen den Anbietern stehen ausschließlich in `internal/adapter/driven/<anbieter>`.

## Einen Anbieter hinzufügen

1. **Adapter** in `internal/adapter/driven/<anbieter>` schreiben. Er erfüllt `cloud.Provider` (`internal/port/driven/cloud`): `ServerByName`, `CreateServer`, `DeleteServer`, `EnsureLoginKey`, `EnsureFirewall`, `Validate` und die Namen für Meldungen. Die Logik arbeitet gegen eine eigene schmale `API`, die SDK-Schicht (`sdk.go`) ist dünn. So lässt sich die Logik ohne Konto testen.
2. **Anbietertabelle** in `internal/model/provider.go`: Name, Pflichteinstellungen (`Credentials`), geheime Umgebungsvariablen (`Secrets`) und die Standardwerte für Typ, Ort und Image.
3. **Verdrahtung** in `internal/app/provider.go` (`NewProvider`): Einstellungen des Rechners in die Optionen des Adapters übersetzen.
4. **Tests:** Fake der `API`, Tests der Logik; für einen echten Lauf einen Test mit Build-Tag `e2e`.
5. **Doku:** Handbuch (Kapitel 4), `.env.local.example`, `machines/example.env.local.example`.

Der Login-Schlüssel muss kein Objekt beim Anbieter sein: `LoginKey.Ref` ist der Name des Schlüssels oder der Schlüssel selbst (GCP, Azure).

## Tests

* `make test` führt alles mit `-race` aus und braucht kein Konto: die Use Cases laufen gegen Fakes (`internal/servertest`), die Adapter gegen nachgebildete APIs.
* Echte Läufe haben den Build-Tag `e2e` und laufen nur mit einer Umgebungsvariablen:
  * `internal/adapter/driven/aws/e2e_test.go`: Schlüsselpaar, Security Group und eine kleine Instanz, danach alles löschen (`VIPER_E2E_AWS=<profil>`).
  * `internal/app/e2e_test.go`: vollständiger Lauf von `up` und `down` mit `DNS_PROVIDER=none`, auch auf ARM (`VIPER_E2E_TYPE=t4g.small`).

  Beide kosten wenige Cent und räumen auch bei einem Fehler auf. Die Anmeldung kommt aus `~/.aws`; im Container muss das Verzeichnis schreibbar sein, damit das SSO-Token erneuert werden kann.
* Die Rechnerdatei `machines/aws-test.env.local` hat dieselben Einstellungen wie der Lauf, für einen Versuch von Hand.
* Die Quellen von `viper find` prüft jede Nacht der Workflow `price sources` (Build-Tag `live`, siehe unten).

## Preisquellen von find

`viper find` liest seine Preise aus öffentlichen Quellen, ohne Konto. Zwei davon sind Webseiten, keine Schnittstelle, und ändern sich ohne Vorwarnung:

| Anbieter | Quelle | Auswertung |
|---|---|---|
| Hetzner | die Seiten unter `hetzner.com/cloud/…` (Ausstattung) und die Datei `cloud_data.json`, die sie laden (Preise je Standort, IPv4) | `internal/adapter/driven/hetzner/public.go`: Text der Seiten mit einem Muster, die Datei als JSON |
| Google Cloud | die Preisseiten der Familien, die Seite der Platten, die Netzwerk-Preisseite | `internal/adapter/driven/gcp/web.go`: Muster über den Datenblock der Seite (Regionsetiketten, Textzellen, Preise), Tabellen sind die Zellen zwischen zwei Etiketten |
| Azure | Daten des Preisrechners (Kerne, RAM) und die Retail Prices API (Preise) | `internal/adapter/driven/azure/catalog.go` |
| AWS | öffentliche Preisdatei je Region (EC2 und Netzwerkdienst) | `internal/adapter/driven/aws/catalog.go`, als Strom gelesen |
| EZB | tägliche Referenzkurse | `internal/adapter/driven/ecb/ecb.go` |

Die Regeln dabei: eine Zeile, die nicht passt, fehlt, sie wird nie geraten; eine Seite, die gar nichts liefert, ist ein Fehler (`did it change?`) und wird nicht gemerkt; das Ergebnis liegt einen Tag im Cache (`internal/adapter/driven/pricecache`), und ein alter Stand gilt ohne Netz höchstens sieben Tage.

### Nächtliche Prüfung

Der Workflow `.github/workflows/sources.yml` (`price sources`) führt jede Nacht um 03:17 UTC die Tests in `internal/live/` aus (Build-Tag `live`; von Hand: `go test -tags live -count=1 -v ./internal/live/`, rund eine Minute). Sie lesen die echten Quellen und prüfen die Form (genug Typen, bekannte Typen darunter, Preise über null, die IPv4-Gebühr), nicht die Preise.

* **Quelle nicht erreichbar** (Netz, Zeitlimit, `403`, `429`, `5xx`; `internal/livecheck`): Der Test überspringt sich. Das sagt nichts über `viper`, und es gibt kein Issue.
* **Quelle antwortet, wird aber nicht mehr gelesen:** Der Test schlägt fehl, und der Workflow legt ein Issue „viper find: Preisquellen lassen sich nicht mehr auswerten“ an (oder kommentiert das offene) mit den Zeilen des Laufs. Die nächste Nacht, in der alle Quellen wieder gelesen werden und keine übersprungen ist, schließt es.

Bei einem Issue: Die Seite im Browser ansehen, das Muster in der Auswertung anpassen (es steht in der Tabelle oben), den Test von Hand laufen lassen und einen Test mit der neuen Form der Seite ergänzen. Geplante Workflows laufen nur auf dem Standard-Branch, und GitHub schaltet sie nach 60 Tagen ohne Aktivität im Repository ab.

## Stand der Anbieter

| Anbieter | Getestet |
|---|---|
| AWS | echte Läufe: Adapter, `up` und `down`, x86 und ARM, ohne Cloudflare |
| Hetzner | nur gegen nachgebildete Schnittstellen (die früheren Bash-Skripte liefen echt) |
| Google Cloud, Azure | nur gegen nachgebildete Schnittstellen; die SDK-Schicht ist vom Compiler geprüft, nicht gegen ein Konto gelaufen |

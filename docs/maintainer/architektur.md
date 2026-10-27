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

## Stand der Anbieter

| Anbieter | Getestet |
|---|---|
| AWS | echte Läufe: Adapter, `up` und `down`, x86 und ARM, ohne Cloudflare |
| Hetzner | nur gegen nachgebildete Schnittstellen (die früheren Bash-Skripte liefen echt) |
| Google Cloud, Azure | nur gegen nachgebildete Schnittstellen; die SDK-Schicht ist vom Compiler geprüft, nicht gegen ein Konto gelaufen |

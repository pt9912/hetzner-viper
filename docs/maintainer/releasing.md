# Release erstellen

Diese Anleitung richtet sich an Maintainer. Ein Release besteht aus dem Git-Tag mit der Release-Seite auf GitHub (das Quellarchiv erzeugt GitHub selbst) und den `viper`-Binaries für Linux und macOS (amd64 und arm64) und Windows (nur amd64) mit `SHA256SUMS`. Die Binaries baut der Workflow `release binaries` (`.github/workflows/release-binaries.yml`), sobald ein Tag der Form `vX.Y.Z` gepusht wird. Er führt zuerst `make check` aus (schlägt es fehl, entsteht kein Binary) und baut dann alle Plattformen mit Go auf einem Runner. Das Windows-Binary heißt `viper_vX.Y.Z_windows_amd64.exe` (die übrigen haben keine Endung). Es gehört nicht zur Homebrew-Formel; der Job `homebrew` liest nur die vier Linux- und macOS-Zeilen aus `SHA256SUMS`. Die Version kommt per `-ldflags` aus dem Tag und erscheint in `viper version`.

## Homebrew

Zu jedem stabilen Release gehört eine Formel. Der Job `homebrew` im Workflow `release binaries` füllt die Vorlage `packaging/homebrew-formula.rb.tmpl` mit den vier Prüfsummen aus der `SHA256SUMS` desselben Tags (fehlt eine, bricht er ab), hängt das Ergebnis als `viper.rb` an das Release und legt es im Tap `pt9912/homebrew-viper` ab (`Formula/viper.rb`). Benutzer installieren mit `brew install pt9912/viper/viper`. Eine Vorabversion (Tag mit `-`) ändert den Tap nicht.

Einmalig einzurichten (Handlung des Betreibers):

1. Das Repository `pt9912/homebrew-viper` anlegen (öffentlich, mit einem ersten Commit).
2. Einen fein granularen Token erzeugen, der nur für dieses Repository gilt (*Contents: Read and write*, mit Ablaufdatum), und ihn im Repository `hetzner-viper` als Geheimnis `HOMEBREW_TAP_GITHUB_TOKEN` hinterlegen (`gh secret set HOMEBREW_TAP_GITHUB_TOKEN`).

Den Job allein (Formel und Tap eines vorhandenen Releases, ohne neue Binaries) starten Sie von Hand: `gh workflow run "release binaries" -f tag=v0.5.1`. Fehlt das Geheimnis, endet der Schritt mit einem Hinweis, das Release bleibt unberührt, und `viper.rb` hängt trotzdem am Release. Vorab prüfen Sie die Vorlage mit `brew audit --strict` in einem lokalen Tap (`brew tap-new --no-git local/probe`, die gefüllte Formel nach `Formula/viper.rb` kopieren).

Probieren Sie die veröffentlichte Formel mit `brew install pt9912/viper/viper` aus, prüfen Sie vorher mit `brew list --versions viper` und `brew tap`, ob `viper` oder der Tap `pt9912/viper` auf diesem Rechner schon installiert sind. `brew uninstall viper` und `brew untap pt9912/viper` entfernen auch eine Installation, die Sie selbst nutzen. Räumen Sie nur auf, was Sie für den Test angelegt haben, und lassen Sie `brew audit --online` bei Bedarf nur gegen den lokalen Test-Tap laufen (es holt `homebrew/core`, rund 1,5 GB).

## Versionsnummer

Die Versionen folgen [SemVer](https://semver.org/lang/de/) mit dem Präfix `v` (`v0.5.0`):

* Solange die Hauptversion `0` ist, kann sich das Verhalten noch ändern. Eine neue Funktion oder eine inkompatible Änderung erhöht die Nebenversion (`v0.5.0`), eine reine Fehlerkorrektur die Patch-Version (`v0.5.1`).
* `v1.0.0` erklärt die Bedienung (Befehle und Einstellungen in `.env.local`) für stabil.

Die Handbuch-Version (`Version:` im Kopf von `docs/user/handbuch.md`) zählt unabhängig davon und steigt mit jeder Änderung am Handbuch. Im Kopf steht bei `Software-Version:` die Release-Version.

## Vorbereitung

1. Wechseln Sie auf `main` und holen Sie den neuesten Stand: `git switch main && git pull`.
2. Der Arbeitsbaum ist sauber (`git status`) und alles ist gepusht.
3. Die Prüfungen laufen lokal mit `make check` (gofmt, `go vet`, golangci-lint, `go test -race`). Alles läuft in Containern aus dem `Dockerfile`; nötig sind nur Docker und `make`. Das Test-Image enthält `bash`, `git`, `jq`, `ssh-keygen`, `sshd` und `shellcheck`; fehlt eines, schlägt der Test fehl. `make help` zeigt alle Ziele. Mit `make build` bauen Sie `viper` vorab für Ihren Rechner (`dist/viper`; `make build VERSION=v0.5.0 GOOS=darwin GOARCH=arm64` für ein anderes Ziel) und prüfen mit `./dist/viper version`. Für Windows bauen Sie mit `make build GOOS=windows GOARCH=amd64`; die Datei heißt dann trotzdem `dist/viper` (ohne `.exe`) und lässt sich nur unter Windows starten.
4. Der letzte Lauf des Workflows `tests` auf `main` ist grün: `gh run list --branch main --limit 3`.
5. **Probelauf gegen echte Dienste** (die Tests laufen nur gegen Fakes): Mit einem kleinen Server (`SERVER_TYPE=cpx02` in `machines/test.env.local`) nacheinander `viper preflight test`, `viper up test`, `viper check test`, `viper ssh test --cmd "echo ok"` und `viper down test`. Das kostet wenige Cent.
6. Das Handbuch ist aktuell (`docs/user/handbuch.md`):
   * `Software-Version:` auf die neue Version setzen und `Stand:` aktualisieren,
   * `Version:` um eins erhöhen und in Kapitel 11 eine Zeile mit der neuen Software-Version und der Änderung für die Nutzer ergänzen,
   * die Dateinamen der Binaries im Abschnitt „`viper` installieren“ auf die neue Version setzen,
   * das als eigenen Commit nach `main` pushen und den CI-Lauf abwarten.

## Release erstellen

Das Tag soll auf den geprüften Stand von `main` zeigen:

```bash
gh release create v0.5.0 --target main --title v0.5.0 --generate-notes
```

`--generate-notes` erzeugt die Release Notes aus den Commits seit dem vorigen Release. Für handgeschriebene Notes: `--notes-file <datei>` statt `--generate-notes`. Mit `--prerelease` markieren Sie eine Vorabversion.

Das Erstellen ist öffentlich sichtbar. Prüfen Sie Version und Ziel vorher.

## Danach prüfen

* Der Workflow `release binaries` ist grün (`gh run list --workflow "release binaries" --limit 1`, bei Bedarf `gh run watch`). Das Release zeigt dann die fünf Binaries und `SHA256SUMS` als Dateien.
* `gh release view v0.5.0` zeigt Tag, Notes, Quellarchiv und die Binaries.
* Ein heruntergeladenes Binary passt zur Prüfsumme und meldet die Version: `grep viper_v0.5.0_linux_amd64 SHA256SUMS | sha256sum --check`, dann `./viper_v0.5.0_linux_amd64 version`. Das Windows-Binary prüfen Sie ebenso mit `grep viper_v0.5.0_windows_amd64.exe SHA256SUMS | sha256sum --check`; starten lässt es sich nur unter Windows (`.\viper_v0.5.0_windows_amd64.exe version`).

## Ein Release korrigieren

* **Notes ändern:** `gh release edit v0.5.0 --notes-file <datei>`.
* **Binaries fehlen oder sind kaputt:** Das Tag nicht verschieben. Den Workflow für das Tag erneut starten (`gh run rerun <id>`; `--clobber` ersetzt vorhandene Dateien) oder eine neue Patch-Version veröffentlichen.
* **Fehlerhaftes Release:** Veröffentlichen Sie eine neue Patch-Version, statt ein vorhandenes Tag zu verschieben. Wer die Version schon heruntergeladen hat, behielte sonst einen anderen Stand als die Release-Seite zeigt.
* **Nur wenn niemand die Version benutzt haben kann:** `gh release delete v0.5.0 --cleanup-tag` entfernt Release und Tag.

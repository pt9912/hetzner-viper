# Release erstellen

Diese Anleitung richtet sich an Maintainer. Ein Release besteht aus vier Teilen: dem Git-Tag mit der Release-Seite auf GitHub (das Quellarchiv erzeugt GitHub selbst), den `viper`-Binaries (Linux und macOS, amd64 und arm64, mit `SHA256SUMS`; gebaut vom Workflow `release binaries`, die Version kommt per `-ldflags` aus dem Tag) und dem Container-Image `ghcr.io/pt9912/hetzner-viper:<version>`. Das Image baut der Workflow `release image` (`.github/workflows/release-image.yml`), sobald ein Tag `v*` gepusht wird (für `linux/amd64` und `linux/arm64`).

Das Image enthält nur die Werkzeuge (`ssh`, `hcloud`, `jq` …), keine Skripte; die kommen aus dem Klon. Auf einem Tag der Form `vX.Y.Z` holt `make` das Image zur Version selbst (`docker pull`), auf jedem anderen Stand baut es es lokal. Ändert sich also nur ein Skript, ändert sich am Image nichts.

## Versionsnummer

Die Versionen folgen [SemVer](https://semver.org/lang/de/) mit dem Präfix `v` (`v0.4.0`):

* Solange die Hauptversion `0` ist, kann sich das Verhalten noch ändern. Eine neue Funktion oder eine inkompatible Änderung erhöht die Nebenversion (`v0.4.0`), eine reine Fehlerkorrektur die Patch-Version (`v0.1.1`).
* `v1.0.0` erklärt die Bedienung (Befehle und Einstellungen in `.env.local`) für stabil.

Die Handbuch-Version (`Version:` im Kopf von `docs/user/handbuch.md`) zählt unabhängig davon und steigt mit jeder Änderung am Handbuch. Im Kopf steht bei `Software-Version:` die Release-Version.

## Vorbereitung

1. Wechseln Sie auf `main` und holen Sie den neuesten Stand: `git switch main && git pull`.
2. Der Arbeitsbaum ist sauber (`git status`) und alles ist gepusht.
3. Die Tests laufen lokal: `make docker-test`.
4. Der letzte Lauf des Workflows `tests` auf `main` ist grün: `gh run list --branch main --limit 3`.
5. Das Handbuch ist aktuell (`docs/user/handbuch.md`):
   * `Software-Version:` auf die neue Version setzen und `Stand:` aktualisieren,
   * `Version:` um eins erhöhen und in Kapitel 11 eine Zeile mit der neuen Software-Version und der Änderung für die Nutzer ergänzen,
   * das als eigenen Commit nach `main` pushen und den CI-Lauf abwarten.

## Release erstellen

Das Tag soll auf den geprüften Stand von `main` zeigen:

```bash
gh release create v0.4.0 --target main --title v0.4.0 --generate-notes
```

`--generate-notes` erzeugt die Release Notes aus den Commits seit dem vorigen Release. Für handgeschriebene Notes: `--notes-file <datei>` statt `--generate-notes`. Mit `--prerelease` markieren Sie eine Vorabversion.

Das Erstellen ist öffentlich sichtbar. Prüfen Sie Version und Ziel vorher.

## Danach prüfen

* Die Workflows `release image` und `release binaries` sind grün (`gh run list --limit 4`, bei Bedarf `gh run watch`). Das Release zeigt dann die Binaries und `SHA256SUMS`. Ohne Image schlägt `make` auf dem Tag mit „Cannot pull …“ fehl; `gh release create` legt das Tag an und startet beide Workflows, kündigen Sie das Release daher erst an, wenn sie durch sind.
* Das Image ist öffentlich lesbar. Bei `v0.2.0` war das Paket sofort öffentlich; sollte GitHub ein Paket doch privat anlegen: Unter *Packages → hetzner-viper → Package settings* die Sichtbarkeit auf *Public* stellen (das lässt sich nicht per `gh` erledigen). Prüfen ohne Anmeldung: `docker logout ghcr.io && docker pull ghcr.io/pt9912/hetzner-viper:v0.4.0`.
* `gh release view v0.4.0` zeigt Tag, Notes, Quellarchiv und die Binaries.
* `git ls-remote --tags origin` listet das Tag.
* Auf dem Tag nutzt `make` das Image: `git clone --branch v0.4.0 …`, dann `make build`; das zieht `ghcr.io/pt9912/hetzner-viper:v0.4.0`.
* Ein frischer Clone der Version funktioniert: `git clone --branch v0.4.0 https://github.com/pt9912/hetzner-viper.git`.

## Ein Release korrigieren

* **Notes ändern:** `gh release edit v0.4.0 --notes-file <datei>`.
* **Image fehlt oder ist kaputt:** Das Tag nicht verschieben. Den Workflow für das Tag erneut starten (`gh run rerun <id>`) oder eine neue Patch-Version veröffentlichen.
* **Fehlerhaftes Release:** Veröffentlichen Sie eine neue Patch-Version, statt ein vorhandenes Tag zu verschieben. Wer die Version schon geklont hat, behielte sonst einen anderen Stand als die Release-Seite zeigt.
* **Nur wenn niemand die Version benutzt haben kann:** `gh release delete v0.4.0 --cleanup-tag` entfernt Release und Tag.

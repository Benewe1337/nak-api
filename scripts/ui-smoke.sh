#!/usr/bin/env bash
# UI smoke test: renders every portal page of `nak serve --demo` in headless
# Chrome and checks that the expected content appears and no script error is
# logged. Usage: scripts/ui-smoke.sh ./nak   (needs google-chrome or chromium)
set -euo pipefail
BIN=${1:-./nak}
PORT=${PORT:-18093}
CHROME=$(command -v google-chrome-stable || command -v google-chrome || command -v chromium || command -v chromium-browser)
PROFILE=$(mktemp -d)
"$BIN" serve --demo --addr 127.0.0.1:$PORT >/tmp/naknak-smoke.log 2>&1 &
SERVER=$!
trap 'kill $SERVER 2>/dev/null; rm -rf "$PROFILE"' EXIT
for i in $(seq 1 50); do curl -sf "http://127.0.0.1:$PORT/healthz" >/dev/null && break; sleep 0.2; done

fail=0
check() { # page expectation…
  local page=$1; shift
  local dom log pf=0
  dom=$(mktemp); log=$(mktemp)
  timeout 60 "$CHROME" --headless=new --no-sandbox --disable-gpu --user-data-dir="$PROFILE" \
    --enable-logging=stderr --v=0 --virtual-time-budget=5000 --dump-dom \
    "http://127.0.0.1:$PORT/?still$page" >"$dom" 2>"$log" || true
  for want in "$@"; do
    if ! grep -qF -- "$want" "$dom"; then echo "FAIL $page: missing \"$want\""; pf=1; fi
  done
  if grep -E 'CONSOLE.*(Uncaught|TypeError|ReferenceError|SyntaxError)' "$log"; then echo "FAIL $page: script error"; pf=1; fi
  if [ $pf = 0 ]; then echo "ok   $page"; else fail=1; fi
  rm -f "$dom" "$log"
}

check "#/"                    "Max." "Als Nächstes" "Fristen" "Übungsblatt 4" "Diese Woche" "Note ausstehend" "Theoretische Informatik"
check "#/woche"               "Softwaretechnik" "B 204"
check "#/kurse"               "Datenbanksysteme" "Tutorium Datenbanksysteme"
check "#/modul/I160"          "Prüfungsverlauf" "Jonas Brandt" "Altklausuren" "Klausur (90 Minuten)"
check "#/modul/I160/inhalt"   "Folien Kapitel 1" "kapitel-1.pdf"
check "#/modul/I160/abgaben"  "Projektgruppen Sprint 3" "Gruppe A" "Abstimmen"
check "#/modul/I168"          "Diskrete Mathematik 2" "4,0 (2.Versuch)"
check "#/modul/I174"          "Statistik" "Mündliche Ergänzungsprüfung zulässig"
check "#/neu"                 "Inbox" "Folien Kapitel 2" "Neue Bewertung"
check "#/nachrichten/71"      "Entwurf bis Freitag" "Vorschau"
check "#/noten"               "Rechnernetze" "von 210 Credits" "Bachelorarbeit" "Studienverlauf" "Notenrechner" "Softwaretechnik" "mündl. Ergänzung"
check "#/pruefungen"          "WP Data Science Basics" "Anmelden" "Geschrieben"
check "#/einstellungen"       "Aussehen" "Startseite oben" "Navigationsleiste" "Kopfleiste" "Gesamtausgaben anzeigen" "Auf der Startseite"
check "#/einstellungen/benachrichtigungen" "Neue Noten schneller melden"
check "#/einstellungen/verbindungen" "EduVault" "Kalender-Abo"
check "#/einstellungen/konto" "Deine Daten" "Daten exportieren" "Zwischenspeicher"
check "#/abgabe/801"          "Übungsblatt 4" "Abgeben"
check "#/studium/seminare"    "Moderation und Präsentation" "Agiles Projektmanagement" "zum Seminar anmelden"
check "#/studium/wahlpflicht" "WP Data Engineering"
check "#/bachelorthesis"      "Bachelorthesis" "Anmeldung noch nicht möglich" "Dein Fahrplan" "Späteste Anmeldung" "Rechner" "Abschluss März 2028" "Betreuungs-Assistent" "Vorschlag finden" "Passt das?" "Gutachtende finden"
check "#/mensa"               "Speiseplan" "Mensakarte" "12,35" "nur vegetarisch" "5,00"
check "#/transferleistungen"   "Transfermodule" "von 30 ECTS" "Bachelorthesis" "1,9" "Kriterienmittel 1,98" "Praxisphase"
check "#/studium/bescheinigungen" "Studienbescheinigung WS 2026" "Deutsch"
check "#/studium/profil"      "Mustermann" "Kopierguthaben" "Kontakt ändern" "Adresse ändern" "Freigaben ändern"
exit $fail

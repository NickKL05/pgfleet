# pgfleet demo recording, rendered with charmbracelet/vhs:
#
#   vhs demo/demo.tape        # produces demo/demo.gif
#
# Prerequisites (so the GIF stays focused on the drift story):
#   docker compose up -d
#   go build -o pgfleet ./cmd/pgfleet
#   PGFLEET_DSN=postgres://pgfleet:pgfleet@localhost:5432/fleet ./pgfleet migrate up

Output demo/demo.gif

Set Shell bash
Set FontSize 16
Set Width 1100
Set Height 700
Set Padding 18
Set Theme "Catppuccin Mocha"

Hide
Type "export PGFLEET_DSN='postgres://pgfleet:pgfleet@localhost:5432/fleet'" Enter
Type "alias pgfleet=./pgfleet" Enter
Type "clear" Enter
Show

Type "pgfleet drift verify" Sleep 600ms Enter
Sleep 2500ms

Type "docker compose exec -T postgres psql -U pgfleet -d fleet -q -f - < demo/introduce_drift.sql" Sleep 400ms Enter
Sleep 1500ms

Type "pgfleet drift verify" Sleep 600ms Enter
Sleep 2500ms

Type "pgfleet drift diff tenant_142" Sleep 600ms Enter
Sleep 2500ms

Type "pgfleet drift repair tenant_087 --out repair/ && cat repair/tenant_087.sql" Sleep 600ms Enter
Sleep 3500ms

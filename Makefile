COMPOSE := docker compose

.PHONY: up down logs ps restart check fmt smoke build-production preview-static db-shell hatchet-token configure-auth init-keyring grant-admin allow-user revoke-admin disable-user test-integration

up:
	$(COMPOSE) up -d --build --wait --wait-timeout 600

down:
	$(COMPOSE) down

logs:
	$(COMPOSE) logs --tail=100 -f

ps:
	$(COMPOSE) ps

restart:
	$(COMPOSE) restart api worker

check:
	$(COMPOSE) config --quiet
	$(COMPOSE) run --rm --no-deps api go test ./...
	$(COMPOSE) run --rm --no-deps api go vet ./...
	$(COMPOSE) run --rm --no-deps web npm test
	$(COMPOSE) run --rm --no-deps web npm run check
	$(COMPOSE) run --rm --no-deps web npm run lint
	$(COMPOSE) run --rm --no-deps web npm run build

fmt:
	$(COMPOSE) run --rm --no-deps api go fmt ./...
	$(COMPOSE) run --rm --no-deps web npm run format

smoke:
	$(COMPOSE) exec -T api wget -qO- http://127.0.0.1:8080/readyz
	$(COMPOSE) exec -T web node -e 'fetch("http://127.0.0.1:5173").then(r=>{if(!r.ok)process.exit(1)}).catch(()=>process.exit(1))'
	$(COMPOSE) run --rm --no-deps devcheck

build-production:
	$(COMPOSE) -f compose.production.yaml build
	$(COMPOSE) run --rm --no-deps web npm run build

preview-static:
	$(COMPOSE) run --rm --no-deps web npm run build
	$(COMPOSE) run --rm --no-deps -p 127.0.0.1:3199:8787 web npx wrangler dev --local --ip 0.0.0.0 --port 8787

db-shell:
	$(COMPOSE) exec postgres psql -U cycrio_migrator -d cycrio

hatchet-token:
	$(COMPOSE) run --rm --no-deps -e HATCHET_ROTATE_TOKEN=true hatchet-init
	$(COMPOSE) restart api worker

configure-auth:
	$(COMPOSE) run --rm --no-deps -T manage configure-auth
	$(COMPOSE) restart api

init-keyring:
	$(COMPOSE) run --rm --no-deps -T manage init-keyring

grant-admin allow-user revoke-admin disable-user:
	$(COMPOSE) run --rm --no-deps -T manage $@ -user-id '$(USER_ID)'

test-integration:
	$(COMPOSE) run --rm --no-deps -e 'DATABASE_URL=postgres://cycrio_migrator:cycrio-migrator-local@postgres:5432/cycrio?sslmode=disable' api go test -tags=integration ./internal/database ./internal/httpapi

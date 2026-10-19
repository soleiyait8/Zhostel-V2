# Common tasks. Integration tests need ADMS_TEST_DATABASE_URL pointing at a
# database whose public schema can be dropped.

.PHONY: test test-unit build frontend up logs backup

test-unit:
	cd backend && go vet ./... && go test ./...

test:
	@test -n "$$ADMS_TEST_DATABASE_URL" || (echo "set ADMS_TEST_DATABASE_URL"; exit 1)
	cd backend && go vet ./... && go test -p 1 -count=1 ./...
	cd frontend && npx tsc --noEmit

build:
	cd backend && go build -o bin/ ./cmd/...
	cd frontend && npm ci && npm run build

up:
	cd deploy && docker compose up -d --build

logs:
	cd deploy && docker compose logs -f --tail 100

backup:
	cd deploy && ./scripts/backup.sh

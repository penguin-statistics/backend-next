updategeoipdb:
	curl -L -o internal/pkg/geoip/data/GeoLite2-Country.mmdb https://github.com/Dreamacro/maxmind-geoip/releases/latest/download/Country.mmdb

dev:
	gow -c -s run .

watchdocs:
	gow -i docs -g swag init --parseDependency --parseInternal --parseDepth 2

clearlogs:
	rm -rf logs/app-*.log.gz
	rm -rf cmd/test/logs/app-*.log.gz
	rm -rf cmd/test/e2e/logs/app-*.log.gz
	rm -rf cmd/test/unit/logs/app-*.log.gz

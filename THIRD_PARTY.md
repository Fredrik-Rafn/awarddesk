# Third-party material

## Airport directory

`internal/airports/airports.json` is a filtered snapshot retrieved 2026-10-20 from https://github.com/datasets/airport-codes/blob/master/data/airport-codes.csv, which derives from OurAirports (https://ourairports.com/data/).

The upstream README identifies the source data as public domain. The subset keeps IATA-coded large, medium and small airports, removes closed facilities and other types, selects one record per IATA code, and stores only code, airport name, municipality and country. There are 8,798 records. This application is not endorsed by OurAirports. Airport records do not establish airline service or award availability.

## Go dependencies

Pinned versions and transitive dependencies are recorded in `go.mod` and `go.sum`. Principal libraries are Hyper Solutions' Go SDK, bogdanfinn/tls-client and fhttp, and golang.org/x/net/html. Their upstream licenses apply. Use `go list -m all` to enumerate the exact build dependency graph.

## Test fixtures

Tests use local and synthetic fixtures. No account credentials, solver keys or browser sessions are included.

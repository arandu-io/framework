module github.com/arandu-io/framework

go 1.25

// O core do arandu nao tem dependencia de terceiros.
// Adapters (postgres, redis, nats) ficam em modulos separados:
//   github.com/arandu-io/framework-postgres
//   github.com/arandu-io/framework-redis
// Isso e uma decisao de produto, nao de estilo: ver docs/adr/0004.
require golang.org/x/crypto v0.31.0

require golang.org/x/sys v0.28.0 // indirect

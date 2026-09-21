# examples/odm

A console walk through the [`odm`](../../odm) package against a real
MongoDB — models and indexes, CRUD and `Save`, querying, relationships,
cursor pagination, soft deletes and transactions, in that order.

```
MONGO_URL=mongodb://localhost:27017 go run ./examples/odm
```

`MONGO_URL` defaults to `mongodb://localhost:27017`. The program writes to a
database called `odm_example` and drops it on the way out.

The transaction section needs a replica set, which is MongoDB's own
requirement rather than this package's. Against a standalone server it prints
why it skipped and carries on.

Unlike [`fiber`](../fiber) and [`nethttp`](../nethttp), this one isn't an
HTTP service: `odm` has nothing to do with HTTP, and a server around it would
only be in the way. Everything it demonstrates is in one file, in the order
you would meet it.

For the same material as compile-checked snippets, see the examples in the
package's own documentation (`go doc github.com/nanaaikinson/chandlery/odm`).

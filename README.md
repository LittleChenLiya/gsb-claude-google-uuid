# uuid
The uuid package generates and inspects UUIDs based on
[RFC 9562](https://datatracker.ietf.org/doc/html/rfc9562)
and DCE 1.1: Authentication and Security Services. 

This package is based on the github.com/pborman/uuid package (previously named
code.google.com/p/go-uuid).  It differs from these earlier packages in that
a UUID is a 16 byte array rather than a byte slice.  One loss due to this
change is the ability to represent an invalid UUID (vs a NIL UUID).

###### Install
```sh
go get github.com/google/uuid
```

###### Documentation 
[![Go Reference](https://pkg.go.dev/badge/github.com/google/uuid.svg)](https://pkg.go.dev/github.com/google/uuid)

Full `go doc` style documentation for the package can be viewed online without
installing this package by using the GoDoc site here: 
https://pkg.go.dev/github.com/google/uuid

###### Strict protocol parsing

`ParseStrict` parses a UUID from an explicit text protocol selected by the
caller. Four protocols are distinguished:

| Format | Example |
| --- | --- |
| `FormatCanonical` | `f47ac10b-58cc-4372-8567-0e02b2c3d479` |
| `FormatURN` | `urn:uuid:f47ac10b-58cc-4372-8567-0e02b2c3d479` |
| `FormatBraced` | `{f47ac10b-58cc-4372-8567-0e02b2c3d479}` |
| `FormatPlain` | `f47ac10b58cc437285670e02b2c3d479` |

The caller chooses the permitted categories via a `Format` bitmask; the
returned value is the canonical UUID and the source format. The entire input
must be a valid UUID in a permitted format — leading or trailing characters are
rejected, not trimmed. Version and variant constraints are checked
independently after parsing. On error the returned UUID is the zero UUID.

```go
u, src, err := uuid.ParseStrict("urn:uuid:f47ac10b-58cc-4372-8567-0e02b2c3d479",
    uuid.ParseOptions{Formats: uuid.FormatCanonical | uuid.FormatURN})
```

The default `Parse` remains unchanged and continues to accept non-standard
forms for backwards compatibility.

Run the targeted tests with:

```sh
go test -run TestStrict -v
go test -run ExampleParseStrict -v
```

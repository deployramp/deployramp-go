# deployramp-go

[![Go Reference](https://pkg.go.dev/badge/github.com/deployramp/deployramp-go.svg)](https://pkg.go.dev/github.com/deployramp/deployramp-go)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

Go SDK for [DeployRamp](https://deployramp.com) — AI-native feature flag management with gradual rollouts, real-time updates, and automatic error-monitored rollbacks.

## Installation

```bash
go get github.com/deployramp/deployramp-go
```

## Quick Start

```go
package main

import (
    "log"
    deployramp "github.com/deployramp/deployramp-go"
)

func main() {
    err := deployramp.Init(deployramp.Config{
        PublicToken: "drp_pub_your_token",
        Traits: map[string]string{
            "plan":   "pro",
            "region": "us-east",
        },
    })
    if err != nil {
        log.Fatal(err)
    }
    defer deployramp.Close()

    // Evaluate a feature flag
    if deployramp.Flag("new-checkout-flow") {
        processNewCheckout()
    } else {
        processOldCheckout()
    }

    // Report errors — DeployRamp uses these to auto-roll back bad deploys
    if err := processCheckout(); err != nil {
        deployramp.Report(err, "new-checkout-flow")
    }
}
```

## Trait-Based Targeting

```go
deployramp.Init(deployramp.Config{PublicToken: "drp_pub_your_token"})

// Update traits after login
deployramp.SetTraits(map[string]string{
    "plan":   "enterprise",
    "cohort": "beta",
})

// Override traits for a single evaluation
enabled := deployramp.Flag("beta-feature", map[string]string{"cohort": "alpha"})
```

## Measure Performance

```go
result := deployramp.MeasureValue("fast-algorithm",
    func() any { return newAlgorithm(data) },
    func() any { return oldAlgorithm(data) },
)
```

## API Reference

| Function | Description |
|---|---|
| `Init(config Config) error` | Initialize the SDK, fetch flags, open WebSocket |
| `Flag(name string, traitOverrides ...map[string]string) bool` | Evaluate a feature flag |
| `SetTraits(traits map[string]string)` | Update user traits for all subsequent evaluations |
| `Measure(name string, enabledFn, disabledFn func(), ...)` | Run branch and record timing |
| `MeasureValue[T](name string, enabledFn, disabledFn func() T, ...) T` | Measure with return value |
| `Report(err error, flagName string, traitOverrides ...map[string]string)` | Report error for rollback monitoring |
| `Close()` | Flush pending events and disconnect |

## Links

- [deployramp.com](https://deployramp.com)
- [GitHub](https://github.com/deployramp/deployramp-go)
- [pkg.go.dev](https://pkg.go.dev/github.com/deployramp/deployramp-go)

## License

MIT

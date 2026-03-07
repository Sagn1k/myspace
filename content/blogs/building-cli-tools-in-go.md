---
title: "Building CLI Tools in Go with Cobra"
date: 2026-02-03
tags: ["go", "cli", "cobra", "developer-tools", "open-source"]
description: "A practical guide to building production-quality CLI tools in Go using the Cobra library, covering project structure, flags, configuration, and testing."
status: published
---

## Why Go for CLI Tools?

Go has become the de facto language for CLI tools in the infrastructure space, and for good reason. Single binary distribution, fast startup time, cross-compilation out of the box, and a rich standard library make it ideal for tools that need to run everywhere without dependency headaches.

Cobra is the most popular framework for building CLI apps in Go. It powers kubectl, Hugo, GitHub CLI, and dozens of other widely-used tools. In this post, I'll walk through building a realistic CLI tool from scratch — a utility called `dq` that queries and formats data from multiple database engines.

## Project Structure

A well-organized Cobra project looks like this:

```
dq/
  cmd/
    root.go
    query.go
    config.go
    version.go
  internal/
    db/
      postgres.go
      mysql.go
      sqlite.go
    formatter/
      table.go
      json.go
      csv.go
  main.go
  go.mod
```

The `cmd/` package holds all command definitions. Each file defines one command (or a group of related subcommands). The `internal/` package holds the business logic that commands delegate to. This separation keeps commands thin — they parse flags, validate input, call internal packages, and handle output.

## The Root Command

Every Cobra app starts with a root command. This is what runs when the user types the binary name with no subcommands:

```go
package cmd

import (
    "fmt"
    "os"

    "github.com/spf13/cobra"
    "github.com/spf13/viper"
)

var cfgFile string

var rootCmd = &cobra.Command{
    Use:   "dq",
    Short: "A universal database query tool",
    Long: `dq lets you run SQL queries against PostgreSQL, MySQL,
and SQLite databases with consistent output formatting.`,
    SilenceUsage: true,
}

func Execute() {
    if err := rootCmd.Execute(); err != nil {
        os.Exit(1)
    }
}

func init() {
    cobra.OnInitialize(initConfig)
    rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "",
        "config file (default is $HOME/.dq.yaml)")
    rootCmd.PersistentFlags().String("format", "table",
        "output format: table, json, csv")
    viper.BindPFlag("format", rootCmd.PersistentFlags().Lookup("format"))
}
```

A few things worth noting here. `SilenceUsage: true` prevents Cobra from dumping the full usage text on every error — you almost always want this. Persistent flags defined on the root command are inherited by all subcommands. And binding flags to Viper lets you also accept values from config files and environment variables.

## Adding the Query Command

The main command is `dq query`, which accepts a connection string and a SQL statement:

```go
var queryCmd = &cobra.Command{
    Use:   "query [connection-string] [sql]",
    Short: "Execute a SQL query and display results",
    Args:  cobra.ExactArgs(2),
    RunE: func(cmd *cobra.Command, args []string) error {
        connStr := args[0]
        sql := args[1]

        timeout, _ := cmd.Flags().GetDuration("timeout")
        limit, _ := cmd.Flags().GetInt("limit")

        conn, err := db.Connect(connStr, timeout)
        if err != nil {
            return fmt.Errorf("connection failed: %w", err)
        }
        defer conn.Close()

        rows, err := conn.Query(sql, limit)
        if err != nil {
            return fmt.Errorf("query failed: %w", err)
        }

        format := viper.GetString("format")
        return formatter.Render(os.Stdout, rows, format)
    },
}

func init() {
    queryCmd.Flags().Duration("timeout", 30*time.Second,
        "connection and query timeout")
    queryCmd.Flags().Int("limit", 0,
        "maximum number of rows to return (0 = unlimited)")
    rootCmd.AddCommand(queryCmd)
}
```

### Use RunE, Not Run

Always prefer `RunE` (which returns an error) over `Run`. This lets you propagate errors up to the root command and handle them consistently, rather than calling `os.Exit(1)` or `log.Fatal` deep inside a subcommand.

## Configuration with Viper

Viper integrates tightly with Cobra to provide a layered configuration system. The precedence order is:

1. Command-line flags (highest priority)
2. Environment variables
3. Config file
4. Default values

Here's the config initialization:

```go
func initConfig() {
    if cfgFile != "" {
        viper.SetConfigFile(cfgFile)
    } else {
        home, err := os.UserHomeDir()
        cobra.CheckErr(err)
        viper.AddConfigPath(home)
        viper.SetConfigName(".dq")
        viper.SetConfigType("yaml")
    }

    viper.SetEnvPrefix("DQ")
    viper.AutomaticEnv()
    viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))

    if err := viper.ReadInConfig(); err == nil {
        fmt.Fprintln(os.Stderr, "Using config:", viper.ConfigFileUsed())
    }
}
```

The `SetEnvPrefix("DQ")` call means environment variables like `DQ_FORMAT=json` will automatically map to the `format` config key. This is the kind of polish that separates a toy CLI from a production tool.

## Testing Commands

Testing Cobra commands is straightforward once you know the pattern. You execute the root command with custom arguments and capture the output:

```go
func executeCommand(args ...string) (string, error) {
    buf := new(bytes.Buffer)
    rootCmd.SetOut(buf)
    rootCmd.SetErr(buf)
    rootCmd.SetArgs(args)

    err := rootCmd.Execute()
    return buf.String(), err
}

func TestQueryCommand_MissingArgs(t *testing.T) {
    _, err := executeCommand("query")
    if err == nil {
        t.Fatal("expected error for missing arguments")
    }
}

func TestVersionCommand(t *testing.T) {
    output, err := executeCommand("version")
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if !strings.Contains(output, "dq version") {
        t.Errorf("unexpected output: %s", output)
    }
}
```

For integration tests against real databases, use `testcontainers-go` to spin up ephemeral Postgres or MySQL containers. This keeps tests hermetic and runnable in CI without external dependencies.

## Distribution

Go's cross-compilation makes distribution trivial. A simple `Makefile` or GoReleaser config can produce binaries for every major platform:

```yaml
# .goreleaser.yaml
builds:
  - binary: dq
    env:
      - CGO_ENABLED=0
    goos: [linux, darwin, windows]
    goarch: [amd64, arm64]
    ldflags:
      - -s -w
      - -X main.version={{.Version}}
      - -X main.commit={{.ShortCommit}}
```

This gives you reproducible, stripped binaries with embedded version information. Pair it with GitHub Actions and you get automated releases on every tag push.

## Final Thoughts

Building CLI tools in Go with Cobra is one of the most satisfying development experiences. The framework handles all the boilerplate — help text, flag parsing, shell completions, argument validation — so you can focus on the actual logic. If you're building developer tools, infrastructure utilities, or internal automation, this stack is hard to beat.

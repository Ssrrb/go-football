# Repository Guidelines

## Project Structure & Module Organization

This repository currently provides a dependency-free Python adapter for official
APF (Paraguayan Football Association) data. Source code lives in
`src/go_football_sources/`: `apf.py` handles APF discovery, HTTP requests, and
payload normalization; `cli.py` exposes the command-line interface. Tests are
in `tests/`. Source research and endpoint decisions belong in `data/sources/`.
Keep new adapters or domain modules under `src/go_football_sources/` and add
matching tests as `tests/test_<module>.py`.

## Build, Test, and Development Commands

The project requires Python 3.11 or newer and uses standard-library tooling.

```bash
PYTHONPATH=src python3 -m unittest discover -s tests -v
PYTHONPATH=src python3 -m go_football_sources.cli discover
PYTHONPATH=src python3 -m go_football_sources.cli round 4 --details finished
```

The first command runs the complete test suite. The latter commands make live,
rate-limited requests to APF; use them for manual checks, not routine unit tests.
For an installed editable package, use `go-football-sources discover` instead.

## Coding Style & Naming Conventions

Use Python 3.11 syntax, four-space indentation, type annotations, and concise
docstrings for public modules, classes, and functions. Follow the existing
`snake_case` names for functions and variables, `PascalCase` for classes, and
`UPPER_SNAKE_CASE` for constants. Keep the adapter dependency-free unless a
change clearly justifies a new dependency. Preserve raw APF payload data while
adding normalized, stable fields; raise `APFError` for source or shape failures.

## Testing Guidelines

Tests use `unittest`. Name test classes by behavior (for example,
`NormalizationTests`) and test methods `test_<expected_behavior>`. Prefer small,
deterministic fixture payloads and client subclasses/mocks over live network
calls. Cover parsing failures and unexpected API shapes when changing extraction
logic.

## Commit & Pull Request Guidelines

History uses short, imperative Conventional Commit-style subjects, e.g.
`feat: add APF round extraction`. Keep commits focused. Pull requests should
explain the behavior change, list commands run, link relevant issues, and include
sample sanitized JSON or CLI output when changing extraction behavior. Update
`data/sources/` documentation when APF endpoint assumptions change.

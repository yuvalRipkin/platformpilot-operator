# PlatformPilot Integration Agent

## Project Overview
A Python-based agentic integration service that extends PlatformPilot by ingesting data from
3rd-party DevOps APIs (GitHub, ArgoCD), processing events in real-time, and running AI agents
that autonomously detect issues and take corrective actions.

This is the fourth repo in the PlatformPilot ecosystem, designed to demonstrate Python backend
proficiency, 3rd-party API integration patterns, and agentic AI — skills required for
Platform Engineering / Backend Integration roles.

## Tech Stack
- **Language:** Python 3.11+
- **Framework:** FastAPI (async-first)
- **APIs:** GitHub REST + GraphQL, ArgoCD REST, webhook listeners
- **Messaging:** Kafka (event processing)
- **Databases:** PostgreSQL (entity catalog), Redis (rate limiting, caching)
- **AI/Agents:** LangGraph multi-agent orchestration, OpenAI/Ollama LLM
- **Config:** pydantic-settings (per-module config pattern)
- **Testing:** pytest, testcontainers
- **Containerization:** Multi-stage Dockerfile, Docker Compose for local stack

## Repository Structure
```
platformpilot-integration-agent/
├── src/
│   ├── main.py                  # FastAPI entry point
│   ├── config.py                # App-level settings (pydantic-settings)
│   ├── core/
│   │   ├── rate_limiter.py      # Redis + Lua token bucket
│   │   └── retry.py             # Exponential backoff with jitter
│   ├── integrations/
│   │   ├── base.py              # BaseIntegration ABC
│   │   ├── github/
│   │   │   ├── client.py        # Async httpx + pagination + rate limiting
│   │   │   ├── models.py        # Pydantic v2 models
│   │   │   ├── config.py        # GitHub-specific settings
│   │   │   └── webhooks.py      # Webhook handler
│   │   └── argocd/
│   │       ├── client.py
│   │       ├── models.py
│   │       └── config.py
│   ├── agents/
│   │   ├── triage.py            # Detects and classifies issues
│   │   ├── diagnosis.py         # Analyzes root cause
│   │   ├── remediation.py       # Proposes and executes fixes
│   │   └── guardrails.py        # Safety constraints + approval workflows
│   ├── db/
│   │   ├── models.py            # SQLAlchemy/Entity models
│   │   └── repository.py        # Data access layer
│   └── api/
│       ├── routes.py            # REST endpoints
│       └── graphql.py           # GraphQL schema
├── tests/
├── docker-compose.yml
├── Dockerfile
├── pyproject.toml
└── README.md
```

## Coding Conventions

### Python Standards
- Type hints everywhere. No `Any` unless truly unavoidable.
- Pydantic v2 models for all request/response schemas and config.
- Dependency injection for services (database, LLM client, integrations).
- Health endpoints: `/health` (liveness), `/ready` (readiness — checks DB + Redis + Kafka).
- Structured logging (structlog or python-json-logger).
- Async where appropriate — FastAPI supports it natively.
- `pyproject.toml` with pinned dependencies (not `requirements.txt`).
- ruff for linting, mypy for type checking.

### Integration Pattern
- Every connector implements `BaseIntegration` ABC (kind, sync, handle_webhook, health_check).
- Per-integration config classes extending pydantic-settings.
- Rate limiting via Redis token bucket on all external API calls.
- Retry with exponential backoff + jitter + Retry-After header respect.
- Pagination handled generically (cursor-based and offset-based).

### Agent Pattern
- Multi-agent orchestration via LangGraph.
- Triage → Diagnosis → Remediation pipeline.
- Confidence thresholds on every agent decision.
- Guardrails: dangerous actions require human approval via API.
- Full audit log of every agent action and decision.

### Git Conventions
- Conventional commits: `feat:`, `fix:`, `docs:`, `chore:`
- Public GitHub repo (not GitLab — this is the public-facing portfolio piece)
- CI via GitHub Actions: lint + type check + unit tests + integration tests + Docker build

## Design Decisions (Locked)
- Config: pydantic-settings with per-module scoping (like Port's Ocean framework)
- Rate limiting: Redis + Lua token bucket (not in-memory — must survive restarts)
- Webhooks: ngrok for local dev, proper ingress for K8s deployment
- LLM: OpenAI for dev, Ollama for on-prem/banking compliance
- This service runs alongside PlatformPilot, not inside it — it's an extension

## Related Repos
- `platformpilot-infra` — Terraform modules (EKS, VPC, RDS)
- `platformpilot-operator` — K8s Operator in Go
- `platformpilot-assistant` — RAG Slack bot (Python/FastAPI/pgvector)

## Available Skills
- `devops-mentor` — learning sessions, concept drilling
- `platformpilot-builder` — code review, architecture guidance
- `devops-interview-prep` — mock interviews, question banks

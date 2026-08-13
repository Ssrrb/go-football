FROM python:3.13-slim

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1

RUN apt-get update \
    && apt-get install --yes --no-install-recommends postgresql-client \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /workspace
COPY etl/pyproject.toml /workspace/etl/pyproject.toml
COPY etl/src /workspace/etl/src
RUN python -m pip install --no-cache-dir /workspace/etl

USER 65532:65532
ENTRYPOINT ["go-football-sources"]
CMD ["discover"]

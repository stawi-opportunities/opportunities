FROM python:3.12-slim AS build
WORKDIR /app

# Build context is the repo root (matches the shared docker-release workflow)
COPY apps/iceberg-ops/pyproject.toml apps/iceberg-ops/requirements.txt /app/
RUN pip install --no-cache-dir --upgrade pip \
 && pip install --no-cache-dir -r requirements.txt

COPY apps/iceberg-ops/src/ /app/src/
RUN pip install --no-cache-dir -e /app

FROM python:3.12-slim
WORKDIR /app
COPY --from=build /usr/local/lib/python3.12/site-packages /usr/local/lib/python3.12/site-packages
COPY --from=build /usr/local/bin /usr/local/bin
COPY --from=build /app /app
ENTRYPOINT ["python", "-m", "stawi_iceberg_ops.nightly"]

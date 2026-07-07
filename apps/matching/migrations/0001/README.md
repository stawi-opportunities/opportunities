# Matching database

GORM owns ordinary matching, application, saved-job, billing, and candidate
state tables. The single SQL file only enables pgvector and its HNSW index,
which GORM cannot express.

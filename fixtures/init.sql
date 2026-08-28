CREATE TABLE repository (
    id SERIAL PRIMARY KEY,
    owner_name VARCHAR(255) NOT NULL,
    name VARCHAR(255) NOT NULL
);

CREATE TABLE push_mirror (
    id SERIAL PRIMARY KEY,
    repo_id INTEGER NOT NULL REFERENCES repository(id),
    remote_address VARCHAR(255) NOT NULL,
    private_key BYTEA
);

INSERT INTO repository (id, owner_name, name) VALUES (1, 'alice', 'example-repo');
INSERT INTO push_mirror (id, repo_id, remote_address, private_key) VALUES (1, 1, 'https://github.com/alice/example-repo.git', 'fake-private-key-bytes');

package store

const schema = `
CREATE TABLE IF NOT EXISTS repos (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    alias TEXT UNIQUE NOT NULL,
    url TEXT NOT NULL,
    paths TEXT NOT NULL,
    commit_sha TEXT,
    indexed_at TEXT,
    doc_count INTEGER DEFAULT 0,
    source_type TEXT NOT NULL DEFAULT 'git',
    status TEXT NOT NULL DEFAULT 'ready',
    status_detail TEXT,
    status_updated_at TEXT
);

CREATE TABLE IF NOT EXISTS documents (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    repo_id INTEGER NOT NULL REFERENCES repos(id),
    path TEXT NOT NULL,
    doc_title TEXT NOT NULL,
    section_title TEXT NOT NULL,
    content TEXT NOT NULL,
    tokens INTEGER NOT NULL,
    heading_level INTEGER,
    has_code INTEGER DEFAULT 0,
    UNIQUE(repo_id, path, section_title)
);

CREATE VIRTUAL TABLE IF NOT EXISTS docs_fts USING fts5(
    doc_title, section_title, content,
    content='documents', content_rowid='id',
    tokenize='porter unicode61'
);

CREATE TRIGGER IF NOT EXISTS docs_ai AFTER INSERT ON documents BEGIN
    INSERT INTO docs_fts(rowid, doc_title, section_title, content)
    VALUES (new.id, new.doc_title, new.section_title, new.content);
END;

CREATE TRIGGER IF NOT EXISTS docs_ad AFTER DELETE ON documents BEGIN
    INSERT INTO docs_fts(docs_fts, rowid, doc_title, section_title, content)
    VALUES ('delete', old.id, old.doc_title, old.section_title, old.content);
END;

CREATE TRIGGER IF NOT EXISTS docs_au AFTER UPDATE ON documents BEGIN
    INSERT INTO docs_fts(docs_fts, rowid, doc_title, section_title, content)
    VALUES ('delete', old.id, old.doc_title, old.section_title, old.content);
    INSERT INTO docs_fts(rowid, doc_title, section_title, content)
    VALUES (new.id, new.doc_title, new.section_title, new.content);
END;
`

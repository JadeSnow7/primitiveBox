-- data-pack seed schema and demo data
-- Run against the SQLite database to pre-populate tables.

CREATE TABLE IF NOT EXISTS products (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    name    TEXT    NOT NULL,
    price   REAL    NOT NULL,
    stock   INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS orders (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    product_id INTEGER NOT NULL REFERENCES products(id),
    quantity   INTEGER NOT NULL,
    placed_at  TEXT    NOT NULL DEFAULT (datetime('now'))
);

INSERT INTO products (name, price, stock) VALUES
    ('Widget A',  9.99,  100),
    ('Widget B', 14.99,   50),
    ('Gadget X', 29.99,   25),
    ('Gadget Y', 49.99,   10);

INSERT INTO orders (product_id, quantity) VALUES
    (1, 3),
    (2, 1),
    (3, 2),
    (1, 5);

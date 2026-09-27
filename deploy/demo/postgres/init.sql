-- Fictional shop data for the Rowbird demo. Every name, email and number here is made up.
-- Orders are dated relative to the moment the database is created, so reports about "yesterday"
-- or "this week" always find rows. The random generator is seeded, so the data is the same on
-- every machine apart from the dates.

SELECT setseed(0.42);

CREATE TABLE categories (
    id   serial PRIMARY KEY,
    name text NOT NULL UNIQUE
);

CREATE TABLE products (
    id          serial PRIMARY KEY,
    sku         text NOT NULL UNIQUE,
    name        text NOT NULL,
    category_id integer NOT NULL REFERENCES categories (id),
    price       numeric(10, 2) NOT NULL,
    stock       integer NOT NULL,
    reorder_at  integer NOT NULL
);

CREATE TABLE customers (
    id         serial PRIMARY KEY,
    name       text NOT NULL,
    email      text NOT NULL UNIQUE,
    region     text NOT NULL,
    created_at timestamptz NOT NULL
);

CREATE TABLE orders (
    id          serial PRIMARY KEY,
    customer_id integer NOT NULL REFERENCES customers (id),
    status      text NOT NULL CHECK (status IN ('paid', 'shipped', 'refunded')),
    created_at  timestamptz NOT NULL,
    total       numeric(12, 2) NOT NULL DEFAULT 0
);

CREATE TABLE order_items (
    order_id   integer NOT NULL REFERENCES orders (id),
    product_id integer NOT NULL REFERENCES products (id),
    quantity   integer NOT NULL,
    unit_price numeric(10, 2) NOT NULL,
    PRIMARY KEY (order_id, product_id)
);

INSERT INTO categories (name) VALUES
    ('Coffee'), ('Tea'), ('Brewing gear'), ('Mugs'), ('Snacks');

INSERT INTO products (sku, name, category_id, price, stock, reorder_at) VALUES
    ('COF-001', 'Mountain blend beans 1 kg',  1, 24.90, 140, 30),
    ('COF-002', 'Espresso roast 500 g',       1, 14.50,  12, 25),
    ('COF-003', 'Decaf beans 500 g',          1, 13.90,  60, 15),
    ('COF-004', 'Single origin sampler',      1, 32.00,   4, 10),
    ('TEA-001', 'Green tea 100 g',            2,  9.80,  85, 20),
    ('TEA-002', 'Earl grey 100 g',            2,  8.90,   7, 20),
    ('TEA-003', 'Chamomile 50 g',             2,  6.50,  40, 10),
    ('GEA-001', 'Pour-over dripper',          3, 29.00,  22,  8),
    ('GEA-002', 'Hand grinder',               3, 64.00,   3,  5),
    ('GEA-003', 'Gooseneck kettle',           3, 49.00,  15,  5),
    ('GEA-004', 'Paper filters x100',         3,  5.90, 300, 50),
    ('MUG-001', 'Stoneware mug',              4, 12.00,  48, 12),
    ('MUG-002', 'Travel tumbler',             4, 22.50,   9, 10),
    ('SNK-001', 'Almond biscotti',            5,  4.20, 120, 30),
    ('SNK-002', 'Dark chocolate bar',         5,  3.80,  18, 25);

-- 200 customers spread over five regions.
INSERT INTO customers (name, email, region, created_at)
SELECT
    initcap(first) || ' ' || initcap(last),
    lower(first) || '.' || lower(last) || n || '@example.com',
    (ARRAY['North', 'South', 'East', 'West', 'Central'])[1 + (n % 5)],
    now() - make_interval(days => 120 + (random() * 600)::int)
FROM generate_series(1, 200) AS n,
LATERAL (SELECT
    (ARRAY['ana', 'bruno', 'carla', 'diego', 'elena', 'felix', 'gabi', 'hugo', 'iris', 'joao',
           'kai', 'lara', 'marco', 'nina', 'otto', 'paula', 'rafa', 'sara', 'theo', 'vera'])[1 + (n % 20)] AS first,
    (ARRAY['silva', 'moreau', 'tanaka', 'okafor', 'novak', 'costa', 'berg', 'rossi', 'khan', 'lopez'])[1 + ((n / 20) % 10)] AS last
) AS names;

-- About 25 orders a day over the last 90 days, including today up to now.
INSERT INTO orders (customer_id, status, created_at)
SELECT
    1 + (random() * 199)::int,
    CASE WHEN random() < 0.04 THEN 'refunded' WHEN day < 3 THEN 'paid' ELSE 'shipped' END,
    date_trunc('day', now()) - make_interval(days => day) + make_interval(secs => (random() * 86399)::int)
FROM generate_series(0, 89) AS day, generate_series(1, 25) AS k;

DELETE FROM orders WHERE created_at > now();

-- One to three distinct products per order.
INSERT INTO order_items (order_id, product_id, quantity, unit_price)
SELECT o.id, p.id, 1 + (random() * 3)::int, p.price
FROM orders o
CROSS JOIN LATERAL (
    SELECT id, price FROM products
    WHERE o.id > 0
    ORDER BY random()
    LIMIT 1 + (random() * 2)::int
) AS p;

UPDATE orders o
SET total = s.total
FROM (SELECT order_id, sum(quantity * unit_price) AS total FROM order_items GROUP BY order_id) AS s
WHERE s.order_id = o.id;

-- Rowbird connects with a read-only login: the recommended setup for any database it reads.
CREATE ROLE rowbird_reader LOGIN PASSWORD 'demo-reader-password';
GRANT USAGE ON SCHEMA public TO rowbird_reader;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO rowbird_reader;
ALTER ROLE rowbird_reader SET default_transaction_read_only = on;

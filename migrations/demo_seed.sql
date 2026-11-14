INSERT INTO halls (id, name, time_zone, width, height)
VALUES (1, 'Основной зал', 'Europe/Moscow', 800, 520),
       (2, 'Терраса', 'Europe/Moscow', 620, 400)
ON CONFLICT (id) DO NOTHING;

INSERT INTO dining_tables (id, hall_id, label, capacity, shape, x, y, size)
VALUES (1, 1, '01', 2, 'square', 100, 80, 66),
       (2, 1, '02', 4, 'square', 290, 80, 88),
       (3, 1, '03', 6, 'round', 520, 240, 126),
       (4, 1, '04', 4, 'round', 180, 330, 96),
       (5, 2, '05', 2, 'round', 200, 120, 70)
ON CONFLICT (id) DO NOTHING;

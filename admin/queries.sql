-- name: CreateUser :one
INSERT INTO admin_users (email, role, password_hash) VALUES ($1, $2, $3) RETURNING id;

-- name: GetUserByEmail :one
SELECT * FROM admin_users WHERE email = $1;

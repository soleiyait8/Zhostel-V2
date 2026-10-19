// Command migrate applies database migrations and, optionally, creates the
// first administrator or loads development seed data.
//
//	migrate                          apply migrations
//	migrate -create-admin a@b.com    create an admin (password from ADMIN_PASSWORD)
//	migrate -seed-dev                load development data (refuses unless APP_ENV=development)
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	_ "time/tzdata" // embed zone data; the runtime image has none

	"github.com/your-org/adms/internal/auth"
	"github.com/your-org/adms/internal/config"
	"github.com/your-org/adms/internal/db"
)

func main() {
	admin := flag.String("create-admin", "", "email of an administrator to create")
	name := flag.String("admin-name", "Administrator", "display name for -create-admin")
	seed := flag.Bool("seed-dev", false, "load development seed data")
	flag.Parse()

	dsn, err := config.Required("DATABASE_URL")
	check(err)
	ctx := context.Background()
	conn, err := db.Open(ctx, dsn)
	check(err)
	applied, err := db.Migrate(ctx, conn)
	check(err)
	if len(applied) == 0 {
		fmt.Println("migrations: up to date")
	} else {
		fmt.Println("migrations applied:", strings.Join(applied, ", "))
	}

	if *admin != "" {
		pw := os.Getenv("ADMIN_PASSWORD")
		check(auth.CheckPolicy(pw))
		hash, err := auth.HashPassword(pw)
		check(err)
		_, err = conn.ExecContext(ctx, `INSERT INTO users (name, email, role, password_hash) VALUES ($1,$2,'admin',$3)`,
			*name, *admin, hash)
		check(err)
		fmt.Println("administrator created:", *admin)
	}

	if *seed {
		if config.String("APP_ENV", "production") != "development" {
			check(fmt.Errorf("refusing to seed: set APP_ENV=development"))
		}
		_, err := conn.ExecContext(ctx, seedSQL)
		check(err)
		fmt.Println("development seed loaded (device SIMMB10VL0001 is SIMULATED)")
	}
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

const seedSQL = `
INSERT INTO departments (name, description) VALUES
 ('Operations','Warehouse and dispatch'), ('Finance','Accounting and payroll'), ('Human Resources','')
ON CONFLICT DO NOTHING;

INSERT INTO employees (employee_number, first_name, last_name, department_id, position, hire_date)
SELECT v.num, v.fn, v.ln, d.id, v.pos, v.hd::date FROM (VALUES
 ('E-0001','Maria','Santos','Operations','Shift Supervisor','2021-03-15'),
 ('E-0002','Jose','Reyes','Operations','Warehouse Associate','2022-07-01'),
 ('E-0003','Ana','Cruz','Finance','Payroll Officer','2020-01-06'),
 ('E-0004','Paolo','Garcia','Operations','Forklift Operator','2023-05-22'),
 ('E-0005','Liza','Mendoza','Human Resources','HR Generalist','2019-11-11')
) AS v(num, fn, ln, dep, pos, hd) JOIN departments d ON d.name = v.dep
ON CONFLICT (employee_number) DO NOTHING;

INSERT INTO biometric_devices (device_name, serial_number, location, model)
VALUES ('Simulated lobby terminal', 'SIMMB10VL0001', 'Development only', 'MB10-VL (SIMULATED)')
ON CONFLICT (serial_number) DO NOTHING;

INSERT INTO employee_device_mappings (employee_id, device_id, device_user_id, valid_from)
SELECT e.id, d.id, v.pin, now() - interval '400 days'
FROM (VALUES ('E-0001','1001'),('E-0002','1002'),('E-0003','1003'),('E-0004','1004')) v(num, pin)
JOIN employees e ON e.employee_number = v.num
JOIN biometric_devices d ON d.serial_number = 'SIMMB10VL0001'
ON CONFLICT DO NOTHING;

INSERT INTO shifts (name, start_time, end_time, break_minutes, grace_period_minutes, overnight) VALUES
 ('Day 08:00–17:00', '08:00', '17:00', 60, 10, false),
 ('Night 22:00–07:00', '22:00', '07:00', 60, 10, true)
ON CONFLICT DO NOTHING;
`

package api

import (
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/your-org/adms/internal/audit"
	"github.com/your-org/adms/internal/httpx"
	"github.com/your-org/adms/internal/protocol"
)

func (a *App) begin(w http.ResponseWriter, r *http.Request) (*sql.Tx, bool) {
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		a.fail(w, err)
		return nil, false
	}
	return tx, true
}

// ------------------------------------------------------------------ departments

type Department struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Active      bool   `json:"active"`
	Employees   int    `json:"employee_count"`
}

func (a *App) listDepartments(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.QueryContext(r.Context(), `SELECT d.id, d.name, d.description, d.active,
		(SELECT count(*) FROM employees e WHERE e.department_id=d.id AND e.employment_status IN ('active','on_leave'))
		FROM departments d ORDER BY d.name`)
	if err != nil {
		a.fail(w, err)
		return
	}
	defer rows.Close()
	out := []Department{}
	for rows.Next() {
		var d Department
		rows.Scan(&d.ID, &d.Name, &d.Description, &d.Active, &d.Employees)
		out = append(out, d)
	}
	httpx.JSON(w, 200, map[string]any{"items": out})
}

type deptIn struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Active      *bool  `json:"active"`
}

func (in *deptIn) validate() Problems {
	p := Problems{}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 100 {
		p.add("name", "enter a department name up to 100 characters")
	}
	if len(in.Description) > 500 {
		p.add("description", "keep the description under 500 characters")
	}
	return p
}

func (a *App) createDepartment(w http.ResponseWriter, r *http.Request) {
	var in deptIn
	if !decode(w, r, &in) || in.validate().write(w) {
		return
	}
	tx, ok := a.begin(w, r)
	if !ok {
		return
	}
	defer tx.Rollback()
	var id string
	if err := tx.QueryRowContext(r.Context(), `INSERT INTO departments (name, description) VALUES ($1,$2) RETURNING id`,
		in.Name, in.Description).Scan(&id); err != nil {
		a.fail(w, err)
		return
	}
	audit.Write(r.Context(), tx, a.entry(r, "create", "department", id, nil, in))
	if err := tx.Commit(); err != nil {
		a.fail(w, err)
		return
	}
	httpx.JSON(w, 201, map[string]string{"id": id})
}

func (a *App) updateDepartment(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in deptIn
	if !decode(w, r, &in) || in.validate().write(w) {
		return
	}
	tx, ok := a.begin(w, r)
	if !ok {
		return
	}
	defer tx.Rollback()
	var before Department
	if err := tx.QueryRowContext(r.Context(), `SELECT id, name, description, active FROM departments WHERE id=$1 FOR UPDATE`, id).
		Scan(&before.ID, &before.Name, &before.Description, &before.Active); err != nil {
		a.fail(w, err)
		return
	}
	active := before.Active
	if in.Active != nil {
		active = *in.Active
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE departments SET name=$2, description=$3, active=$4, updated_at=now() WHERE id=$1`,
		id, in.Name, in.Description, active); err != nil {
		a.fail(w, err)
		return
	}
	audit.Write(r.Context(), tx, a.entry(r, "update", "department", id, before, in))
	if err := tx.Commit(); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(204)
}

// ------------------------------------------------------------------ employees

type Employee struct {
	ID               string    `json:"id"`
	EmployeeNumber   string    `json:"employee_number"`
	FirstName        string    `json:"first_name"`
	LastName         string    `json:"last_name"`
	DepartmentID     *string   `json:"department_id"`
	DepartmentName   *string   `json:"department_name"`
	Position         string    `json:"position"`
	EmploymentStatus string    `json:"employment_status"`
	HireDate         *string   `json:"hire_date"`
	DeviceUserIDs    []string  `json:"device_user_ids"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

const employeeSelect = `SELECT e.id, e.employee_number, e.first_name, e.last_name, e.department_id, d.name,
	e.position, e.employment_status, to_char(e.hire_date,'YYYY-MM-DD'),
	coalesce((SELECT array_agg(DISTINCT m.device_user_id) FROM employee_device_mappings m
	          WHERE m.employee_id=e.id AND m.valid_until IS NULL), '{}'),
	e.created_at, e.updated_at
	FROM employees e LEFT JOIN departments d ON d.id=e.department_id`

func scanEmployee(sc interface{ Scan(...any) error }) (Employee, error) {
	var e Employee
	var dep, depName, hire sql.NullString
	err := sc.Scan(&e.ID, &e.EmployeeNumber, &e.FirstName, &e.LastName, &dep, &depName,
		&e.Position, &e.EmploymentStatus, &hire, pq.Array(&e.DeviceUserIDs), &e.CreatedAt, &e.UpdatedAt)
	e.DepartmentID, e.DepartmentName, e.HireDate = strPtr(dep), strPtr(depName), strPtr(hire)
	if e.DeviceUserIDs == nil {
		e.DeviceUserIDs = []string{}
	}
	return e, err
}

func (a *App) listEmployees(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, offset := limitOffset(r, 50, 500)
	where, args := []string{"true"}, []any{}
	arg := func(v any) string { args = append(args, v); return fmt.Sprintf("$%d", len(args)) }
	if s := strings.TrimSpace(q.Get("q")); s != "" {
		p := arg("%" + strings.ToLower(s) + "%")
		where = append(where, fmt.Sprintf(`(lower(e.first_name||' '||e.last_name) LIKE %s OR lower(e.employee_number) LIKE %s)`, p, p))
	}
	if d := q.Get("department_id"); validUUID(d) {
		where = append(where, "e.department_id="+arg(d))
	}
	if s := q.Get("status"); s != "" {
		where = append(where, "e.employment_status="+arg(s))
	}
	cond := strings.Join(where, " AND ")
	var total int
	if err := a.DB.QueryRowContext(r.Context(), `SELECT count(*) FROM employees e WHERE `+cond, args...).Scan(&total); err != nil {
		a.fail(w, err)
		return
	}
	rows, err := a.DB.QueryContext(r.Context(), employeeSelect+` WHERE `+cond+
		fmt.Sprintf(` ORDER BY e.last_name, e.first_name LIMIT %d OFFSET %d`, limit, offset), args...)
	if err != nil {
		a.fail(w, err)
		return
	}
	defer rows.Close()
	out := []Employee{}
	for rows.Next() {
		e, err := scanEmployee(rows)
		if err != nil {
			a.fail(w, err)
			return
		}
		out = append(out, e)
	}
	httpx.JSON(w, 200, map[string]any{"items": out, "total": total, "limit": limit, "offset": offset})
}

func (a *App) loadEmployee(ctx context.Context, x interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string) (Employee, error) {
	return scanEmployee(x.QueryRowContext(ctx, employeeSelect+` WHERE e.id=$1`, id))
}

func (a *App) getEmployee(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	e, err := a.loadEmployee(r.Context(), a.DB, id)
	if err != nil {
		a.fail(w, err)
		return
	}
	httpx.JSON(w, 200, e)
}

type employeeIn struct {
	EmployeeNumber   string  `json:"employee_number"`
	FirstName        string  `json:"first_name"`
	LastName         string  `json:"last_name"`
	DepartmentID     *string `json:"department_id"`
	Position         string  `json:"position"`
	EmploymentStatus string  `json:"employment_status"`
	HireDate         *string `json:"hire_date"`
}

var empNumRe = regexp.MustCompile(`^[A-Za-z0-9-]{1,32}$`)
var statuses = map[string]bool{"active": true, "on_leave": true, "inactive": true, "separated": true}

func (in *employeeIn) validate() Problems {
	p := Problems{}
	in.EmployeeNumber = strings.TrimSpace(in.EmployeeNumber)
	in.FirstName, in.LastName = strings.TrimSpace(in.FirstName), strings.TrimSpace(in.LastName)
	in.Position = strings.TrimSpace(in.Position)
	if !empNumRe.MatchString(in.EmployeeNumber) {
		p.add("employee_number", "use 1–32 letters, digits or hyphens")
	}
	if in.FirstName == "" || len(in.FirstName) > 100 {
		p.add("first_name", "enter a first name")
	}
	if in.LastName == "" || len(in.LastName) > 100 {
		p.add("last_name", "enter a last name")
	}
	if len(in.Position) > 100 {
		p.add("position", "keep the position under 100 characters")
	}
	if in.EmploymentStatus == "" {
		in.EmploymentStatus = "active"
	}
	if !statuses[in.EmploymentStatus] {
		p.add("employment_status", "choose active, on_leave, inactive or separated")
	}
	if in.DepartmentID != nil && *in.DepartmentID != "" && !validUUID(*in.DepartmentID) {
		p.add("department_id", "choose a department from the list")
	}
	if in.HireDate != nil && *in.HireDate != "" {
		if _, err := time.Parse("2006-01-02", *in.HireDate); err != nil {
			p.add("hire_date", "use the format YYYY-MM-DD")
		}
	}
	return p
}

func (a *App) createEmployee(w http.ResponseWriter, r *http.Request) {
	var in employeeIn
	if !decode(w, r, &in) || in.validate().write(w) {
		return
	}
	tx, ok := a.begin(w, r)
	if !ok {
		return
	}
	defer tx.Rollback()
	var id string
	err := tx.QueryRowContext(r.Context(), `INSERT INTO employees
		(employee_number, first_name, last_name, department_id, position, employment_status, hire_date)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
		in.EmployeeNumber, in.FirstName, in.LastName, nullStr(in.DepartmentID), in.Position,
		in.EmploymentStatus, nullStr(in.HireDate)).Scan(&id)
	if err != nil {
		a.fail(w, err)
		return
	}
	audit.Write(r.Context(), tx, a.entry(r, "create", "employee", id, nil, in))
	if err := tx.Commit(); err != nil {
		a.fail(w, err)
		return
	}
	httpx.JSON(w, 201, map[string]string{"id": id})
}

func (a *App) updateEmployee(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in employeeIn
	if !decode(w, r, &in) || in.validate().write(w) {
		return
	}
	tx, ok := a.begin(w, r)
	if !ok {
		return
	}
	defer tx.Rollback()
	before, err := a.loadEmployee(r.Context(), tx, id)
	if err != nil {
		a.fail(w, err)
		return
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE employees SET employee_number=$2, first_name=$3, last_name=$4,
		department_id=$5, position=$6, employment_status=$7, hire_date=$8, updated_at=now() WHERE id=$1`,
		id, in.EmployeeNumber, in.FirstName, in.LastName, nullStr(in.DepartmentID), in.Position,
		in.EmploymentStatus, nullStr(in.HireDate)); err != nil {
		a.fail(w, err)
		return
	}
	audit.Write(r.Context(), tx, a.entry(r, "update", "employee", id, before, in))
	if err := tx.Commit(); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(204)
}

// deactivateEmployee keeps all history, marks the employee inactive and
// closes active device mappings so the device user ID can be reassigned.
func (a *App) deactivateEmployee(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Status string `json:"employment_status"`
	}
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	if in.Status == "" {
		in.Status = "inactive"
	}
	if in.Status != "inactive" && in.Status != "separated" {
		httpx.Error(w, 422, "validation_failed", "status must be inactive or separated")
		return
	}
	tx, ok := a.begin(w, r)
	if !ok {
		return
	}
	defer tx.Rollback()
	before, err := a.loadEmployee(r.Context(), tx, id)
	if err != nil {
		a.fail(w, err)
		return
	}
	tx.ExecContext(r.Context(), `UPDATE employees SET employment_status=$2, updated_at=now() WHERE id=$1`, id, in.Status)
	res, _ := tx.ExecContext(r.Context(), `UPDATE employee_device_mappings SET valid_until=now()
		WHERE employee_id=$1 AND valid_until IS NULL`, id)
	closed, _ := res.RowsAffected()
	audit.Write(r.Context(), tx, a.entry(r, "deactivate", "employee", id, before,
		map[string]any{"employment_status": in.Status, "mappings_closed": closed}))
	if err := tx.Commit(); err != nil {
		a.fail(w, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"mappings_closed": closed})
}

// ------------------------------------------------------------------ CSV

var csvHeader = []string{"employee_number", "first_name", "last_name", "department", "position", "employment_status", "hire_date"}

// csvSafe neutralises spreadsheet formula injection.
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

func (a *App) exportEmployees(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.QueryContext(r.Context(), `SELECT e.employee_number, e.first_name, e.last_name, coalesce(d.name,''),
		e.position, e.employment_status, coalesce(to_char(e.hire_date,'YYYY-MM-DD'),'')
		FROM employees e LEFT JOIN departments d ON d.id=e.department_id ORDER BY e.employee_number`)
	if err != nil {
		a.fail(w, err)
		return
	}
	defer rows.Close()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="employees.csv"`)
	cw := csv.NewWriter(w)
	cw.Write(csvHeader)
	for rows.Next() {
		rec := make([]string, 7)
		ptrs := make([]any, 7)
		for i := range rec {
			ptrs[i] = &rec[i]
		}
		rows.Scan(ptrs...)
		for i := range rec {
			rec[i] = csvSafe(rec[i])
		}
		cw.Write(rec)
	}
	cw.Flush()
	audit.Write(r.Context(), a.DB, a.entry(r, "export", "employee", "", nil, map[string]string{"format": "csv"}))
}

type importRow struct {
	Line   int    `json:"line"`
	Number string `json:"employee_number"`
	Action string `json:"action"` // created | updated | error
	Error  string `json:"error,omitempty"`
}

// importEmployees upserts by employee_number from a CSV body. The whole file
// is applied in one transaction; any error rolls everything back. Pass
// ?dry_run=true to validate without saving.
func (a *App) importEmployees(w http.ResponseWriter, r *http.Request) {
	dry := r.URL.Query().Get("dry_run") == "true"
	cr := csv.NewReader(io.LimitReader(r.Body, 5<<20))
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	cr.LazyQuotes = true // tolerate stray quotes from hand-edited spreadsheets
	head, err := cr.Read()
	if err != nil {
		httpx.Error(w, 400, "invalid_csv", "the file is empty or not CSV")
		return
	}
	col := map[string]int{}
	for i, h := range head {
		col[strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")))] = i
	}
	for _, need := range []string{"employee_number", "first_name", "last_name"} {
		if _, ok := col[need]; !ok {
			httpx.Error(w, 400, "invalid_csv", "missing required column "+need)
			return
		}
	}
	tx, ok := a.begin(w, r)
	if !ok {
		return
	}
	defer tx.Rollback()
	deps := map[string]string{}
	drows, _ := tx.QueryContext(r.Context(), `SELECT lower(name), id FROM departments`)
	for drows.Next() {
		var n, id string
		drows.Scan(&n, &id)
		deps[n] = id
	}
	drows.Close()

	var results []importRow
	failed := false
	line := 1
	for {
		rec, err := cr.Read()
		line++
		if err == io.EOF {
			break
		}
		if err != nil {
			results = append(results, importRow{Line: line, Action: "error", Error: "unreadable row"})
			failed = true
			continue
		}
		get := func(k string) string {
			if i, ok := col[k]; ok && i < len(rec) {
				return strings.TrimPrefix(strings.TrimSpace(rec[i]), "'")
			}
			return ""
		}
		in := employeeIn{EmployeeNumber: get("employee_number"), FirstName: get("first_name"),
			LastName: get("last_name"), Position: get("position"), EmploymentStatus: get("employment_status")}
		if h := get("hire_date"); h != "" {
			in.HireDate = &h
		}
		row := importRow{Line: line, Number: in.EmployeeNumber}
		if dn := get("department"); dn != "" {
			id, ok := deps[strings.ToLower(dn)]
			if !ok {
				row.Action, row.Error, failed = "error", "unknown department "+dn, true
				results = append(results, row)
				continue
			}
			in.DepartmentID = &id
		}
		if p := in.validate(); len(p) > 0 {
			for f, m := range p {
				row.Error = f + ": " + m
				break
			}
			row.Action, failed = "error", true
			results = append(results, row)
			continue
		}
		var inserted bool
		err = tx.QueryRowContext(r.Context(), `INSERT INTO employees
			(employee_number, first_name, last_name, department_id, position, employment_status, hire_date)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (employee_number) DO UPDATE SET first_name=EXCLUDED.first_name, last_name=EXCLUDED.last_name,
			  department_id=EXCLUDED.department_id, position=EXCLUDED.position,
			  employment_status=EXCLUDED.employment_status, hire_date=EXCLUDED.hire_date, updated_at=now()
			RETURNING (xmax = 0)`,
			in.EmployeeNumber, in.FirstName, in.LastName, nullStr(in.DepartmentID), in.Position,
			in.EmploymentStatus, nullStr(in.HireDate)).Scan(&inserted)
		if err != nil {
			a.fail(w, err)
			return
		}
		row.Action = map[bool]string{true: "created", false: "updated"}[inserted]
		results = append(results, row)
	}
	if results == nil {
		results = []importRow{}
	}
	if failed || dry {
		status := 200
		if failed {
			status = 422
		}
		httpx.JSON(w, status, map[string]any{"saved": false, "rows": results})
		return
	}
	audit.Write(r.Context(), tx, a.entry(r, "import", "employee", "", nil, map[string]int{"rows": len(results)}))
	if err := tx.Commit(); err != nil {
		a.fail(w, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"saved": true, "rows": results})
}

// ------------------------------------------------------------------ mappings

type Mapping struct {
	ID           string     `json:"id"`
	EmployeeID   string     `json:"employee_id"`
	DeviceID     string     `json:"device_id"`
	DeviceName   string     `json:"device_name"`
	DeviceUserID string     `json:"device_user_id"`
	ValidFrom    time.Time  `json:"valid_from"`
	ValidUntil   *time.Time `json:"valid_until"`
	CreatedAt    time.Time  `json:"created_at"`
}

func (a *App) listMappings(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	rows, err := a.DB.QueryContext(r.Context(), `SELECT m.id, m.employee_id, m.device_id, d.device_name, m.device_user_id,
		m.valid_from, m.valid_until, m.created_at
		FROM employee_device_mappings m JOIN biometric_devices d ON d.id=m.device_id
		WHERE m.employee_id=$1 ORDER BY m.valid_until IS NULL DESC, m.valid_from DESC`, id)
	if err != nil {
		a.fail(w, err)
		return
	}
	defer rows.Close()
	out := []Mapping{}
	for rows.Next() {
		var m Mapping
		var vu sql.NullTime
		rows.Scan(&m.ID, &m.EmployeeID, &m.DeviceID, &m.DeviceName, &m.DeviceUserID, &m.ValidFrom, &vu, &m.CreatedAt)
		m.ValidUntil = timePtr(vu)
		out = append(out, m)
	}
	httpx.JSON(w, 200, map[string]any{"items": out})
}

func (a *App) createMapping(w http.ResponseWriter, r *http.Request) {
	empID, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		DeviceID     string     `json:"device_id"`
		DeviceUserID string     `json:"device_user_id"`
		ValidFrom    *time.Time `json:"valid_from"`
	}
	if !decode(w, r, &in) {
		return
	}
	p := Problems{}
	if !validUUID(in.DeviceID) {
		p.add("device_id", "choose a device")
	}
	in.DeviceUserID = strings.TrimSpace(in.DeviceUserID)
	if !protocol.ValidUserID(in.DeviceUserID) {
		p.add("device_user_id", "use the user ID shown on the device (1–24 letters or digits)")
	}
	if p.write(w) {
		return
	}
	from := time.Now()
	if in.ValidFrom != nil {
		from = *in.ValidFrom
	}
	tx, ok := a.begin(w, r)
	if !ok {
		return
	}
	defer tx.Rollback()
	var id string
	err := tx.QueryRowContext(r.Context(), `INSERT INTO employee_device_mappings
		(employee_id, device_id, device_user_id, valid_from, created_by) VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		empID, in.DeviceID, in.DeviceUserID, from, actor(r)).Scan(&id)
	var pe *pq.Error
	if errors.As(err, &pe) && pe.Code == "23P01" {
		tx.Rollback()
		var who string
		a.DB.QueryRowContext(r.Context(), `SELECT e.employee_number||' '||e.first_name||' '||e.last_name
			FROM employee_device_mappings m JOIN employees e ON e.id=m.employee_id
			WHERE m.device_id=$1 AND m.device_user_id=$2 AND m.valid_until IS NULL`, in.DeviceID, in.DeviceUserID).Scan(&who)
		msg := "device user ID " + in.DeviceUserID + " is already mapped on this device for that period"
		if who != "" {
			msg += " (to " + who + "); end that mapping first"
		}
		httpx.Error(w, 409, "mapping_conflict", msg)
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	audit.Write(r.Context(), tx, a.entry(r, "create", "employee_device_mapping", id, nil, in))
	if err := tx.Commit(); err != nil {
		a.fail(w, err)
		return
	}
	httpx.JSON(w, 201, map[string]string{"id": id})
}

func (a *App) endMapping(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	tx, ok := a.begin(w, r)
	if !ok {
		return
	}
	defer tx.Rollback()
	var until time.Time
	err := tx.QueryRowContext(r.Context(), `UPDATE employee_device_mappings SET valid_until=greatest(now(), valid_from + interval '1 second')
		WHERE id=$1 AND valid_until IS NULL RETURNING valid_until`, id).Scan(&until)
	if err != nil {
		a.fail(w, err)
		return
	}
	audit.Write(r.Context(), tx, a.entry(r, "end", "employee_device_mapping", id, nil, map[string]any{"valid_until": until}))
	if err := tx.Commit(); err != nil {
		a.fail(w, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"valid_until": until})
}

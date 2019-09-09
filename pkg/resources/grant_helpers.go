package resources

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/hashicorp/terraform/helper/schema"
	"github.com/jmoiron/sqlx"

	"github.com/chanzuckerberg/terraform-provider-snowflake/pkg/snowflake"
)

// grant represents a generic grant of a privilege from a grant (the target) to a
// grantee. This type can be used in conjunction with github.com/jmoiron/sqlx to
// build a nice go representation of a grant
type grant struct {
	CreatedOn   time.Time `db:"created_on"`
	Privilege   string    `db:"privilege"`
	GrantType   string    `db:"granted_on"`
	GrantName   string    `db:"name"`
	GranteeType string    `db:"granted_to"`
	GranteeName string    `db:"grantee_name"`
	GrantOption bool      `db:"grant_option"`
	GrantedBy   string    `db:"granted_by"`
}

// splitGrantID takes the <db_name>|<schema_name>|<view_name>|<privilege> ID and
// returns the object name and privilege.
func splitGrantID(v string) (string, string, string, string, error) {
	arr := strings.Split(v, "|")
	if len(arr) != 4 {
		return "", "", "", "", fmt.Errorf("ID %v is invalid", v)
	}

	return arr[0], arr[1], arr[2], arr[3], nil
}

func createGenericGrant(data *schema.ResourceData, meta interface{}, builder *snowflake.GrantBuilder) error {
	db := meta.(*sql.DB)

	priv := data.Get("privilege").(string)

	roles, shares := expandRolesAndShares(data)

	if len(roles)+len(shares) == 0 {
		return fmt.Errorf("no roles or shares specified for this grant")
	}

	for _, role := range roles {
		err := DBExec(db, builder.Role(role).Grant(priv))
		if err != nil {
			return err
		}
	}

	for _, share := range shares {
		err := DBExec(db, builder.Share(share).Grant(priv))
		if err != nil {
			return err
		}
	}

	return nil
}

func d(in interface{}) {
	log.Printf("[DEBUG]%#v\n", in)
}

func readGenericGrant(data *schema.ResourceData, meta interface{}, builder *snowflake.GrantBuilder) error {
	grants, err := readGenericGrants(meta, builder)
	if err != nil {
		return err
	}
	priv := data.Get("privilege").(string)

	rolesIn, sharesIn := expandRolesAndShares(data)

	var roles, shares []string
	d("foo")
	for _, grant := range grants {
		// Skip if wrong privilege
		if grant.Privilege != priv {
			continue
		}
		d(grant)
		switch grant.GranteeType {
		case "ROLE":
			if !stringInSlice(grant.GranteeName, rolesIn) {
				continue
			}
			roles = append(roles, grant.GranteeName)
		case "SHARE":
			// Shares get the account appended to their name, remove this
			granteeName := StripAccountFromName(grant.GranteeName)
			if !stringInSlice(granteeName, sharesIn) {
				continue
			}

			shares = append(shares, granteeName)
		default:
			return fmt.Errorf("unknown grantee type %s", grant.GranteeType)
		}
	}

	err = data.Set("privilege", priv)
	if err != nil {
		return err
	}

	err = data.Set("roles", roles)
	if err != nil {
		return err
	}

	err = data.Set("shares", shares)
	if err != nil {
		// warehouses don't use shares - check for this error
		if !strings.HasPrefix(err.Error(), "Invalid address to set") {
			return err
		}
	}

	return nil
}

func readGenericGrants(meta interface{}, builder *snowflake.GrantBuilder) ([]*grant, error) {
	db := meta.(*sql.DB)
	var grants []*grant
	retry := true
	loop_index := 0
	for retry {
		d("Main loop in readGenericGrants()")

		loop_index += 1
		d(fmt.Sprintf("Loop index: %d", loop_index))

		retry = false

		stats := db.Stats()
		d(fmt.Sprintf("db.Stats(): %+v", stats))

		conn := sqlx.NewDb(db, "snowflake")

		stmt := builder.Show()
		log.Printf("[DEBUG] stmt %s", stmt)

		var rows *sqlx.Rows
		var err error
		//c1 := make(chan *sqlx.Rows)
		//go func() {
		//	rows, err = conn.Queryx(stmt)
		//	c1 <- rows
		//	return
		//}()
		//select {
		//case <-c1:
		//	d("conn.Queryx() returned within time.")
		//case <-time.After(12 * time.Second):
		//	d("12 seconds elasped on conn.Queryx(), timing out.")
		//	close(c1)
		//	retry = true
		//}
		rows, err = conn.Queryx(stmt)
		if err != nil {
			return nil, err
		}
		if retry {
			d("Attempting db.Stats()...")
			stats := db.Stats()
			d(fmt.Sprintf("db.Stats(): %+v", stats))
			d("Attempting db.Ping()...")
			db.Ping()
			d("db.Ping() succeeded.")
			continue
		}
		defer rows.Close()

		//next := func(rows *sqlx.Rows) bool {
		//	c2 := make(chan bool)
		//	go func() { c2 <- rows.Next() }()
		//	select {
		//	case res := <-c2:
		//		return res
		//	case <-time.After(12 * time.Second):
		//		d("12 seconds elasped on rows.Next(), timing out.")
		//		close(c2)
		//		retry = true
		//		d("Attempting db.Stats()...")
		//		stats := db.Stats()
		//		d(fmt.Sprintf("db.Stats(): %+v", stats))
		//		d("Attempting db.Ping()...")
		//		db.Ping()
		//		d("db.Ping() succeeded.")
		//		return false
		//	}
		//}

		d("About to fetch first row of query result...")
		grants = nil
		for rows.Next() {
			d("Begin iteration.")
			grant := &grant{}
			err := rows.StructScan(grant)
			if err != nil {
				return nil, err
			}
			d(fmt.Sprintf("Scanned grant: %+v", grant))
			grants = append(grants, grant)
			d("Appended grant, iteration complete.")
		}
		d("Attempting rows.Close().")
		rows.Close()
	}

	d(fmt.Sprintf("Successfully returning from readGenericGrants() with %d scanned grants.", len(grants)))
	return grants, nil
}

func deleteGenericGrant(data *schema.ResourceData, meta interface{}, builder *snowflake.GrantBuilder) error {
	db := meta.(*sql.DB)

	priv := data.Get("privilege").(string)

	var roles, shares []string
	if _, ok := data.GetOk("roles"); ok {
		roles = expandStringList(data.Get("roles").(*schema.Set).List())
	}

	if _, ok := data.GetOk("shares"); ok {
		shares = expandStringList(data.Get("shares").(*schema.Set).List())
	}

	for _, role := range roles {
		err := DBExec(db, builder.Role(role).Revoke(priv))
		if err != nil {
			return err
		}
	}

	for _, share := range shares {
		err := DBExec(db, builder.Share(share).Revoke(priv))
		if err != nil {
			return err
		}
	}

	data.SetId("")
	return nil
}

func expandRolesAndShares(data *schema.ResourceData) ([]string, []string) {
	var roles, shares []string
	if _, ok := data.GetOk("roles"); ok {
		roles = expandStringList(data.Get("roles").(*schema.Set).List())
	}

	if _, ok := data.GetOk("shares"); ok {
		shares = expandStringList(data.Get("shares").(*schema.Set).List())
	}
	return roles, shares
}

func stringInSlice(v string, sl []string) bool {
	for _, s := range sl {
		if s == v {
			return true
		}
	}
	return false
}

package resources

import (
	"database/sql"
	"fmt"
	"log"
	"strings"

	"github.com/chanzuckerberg/terraform-provider-snowflake/pkg/snowflake"
	"github.com/hashicorp/terraform/helper/hashcode"
	"github.com/hashicorp/terraform/helper/schema"
	"github.com/jmoiron/sqlx"
)

func RoleGrants() *schema.Resource {
	return &schema.Resource{
		Create: CreateRoleGrants,
		Read:   ReadRoleGrants,
		Delete: DeleteRoleGrants,
		Update: UpdateRoleGrants,

		Schema: map[string]*schema.Schema{
			"name": &schema.Schema{
				Type:     schema.TypeString,
				Required: true,
			},
			"role_name": &schema.Schema{
				Type: schema.TypeString,
				Elem: &schema.Schema{Type: schema.TypeString},
				Set: func(v interface{}) int {
					return hashcode.String(strings.ToUpper(v.(string)))
				},
				Required:    true,
				Description: "The name of the role we are granting.",
				ValidateFunc: func(val interface{}, key string) ([]string, []error) {
					return snowflake.ValidateIdentifier(val)
				},
			},
			"roles": &schema.Schema{
				Type: schema.TypeSet,
				Elem: &schema.Schema{Type: schema.TypeString},
				Set: func(v interface{}) int {
					return hashcode.String(strings.ToUpper(v.(string)))
				},
				Optional:    true,
				Description: "Grants role to this specified role.",
			},
			"users": &schema.Schema{
				Type: schema.TypeSet,
				Elem: &schema.Schema{Type: schema.TypeString},
				Set: func(v interface{}) int {
					return hashcode.String(strings.ToUpper(v.(string)))
				},
				Optional:    true,
				Description: "Grants role to this specified user.",
			},
		},
		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},
	}
}

func CreateRoleGrants(data *schema.ResourceData, meta interface{}) error {
	db := meta.(*sql.DB)
	log.Printf("[DEBUG] data %#v", data)
	name := data.Get("name").(string)
	roleName := data.Get("role_name").(string)
	roles := expandStringList(data.Get("roles").(*schema.Set).List())
	users := expandStringList(data.Get("users").(*schema.Set).List())

	log.Printf("[DEBUG] role_name %#v", roleName)
	log.Printf("[DEBUG] roles %#v", roles)
	log.Printf("[DEBUG] users %#v", users)

	if len(roles) == 0 && len(users) == 0 {
		return fmt.Errorf("No users or roles specified for role grants.")
	}

	grantRoleToRoles(db, roleName, roles)
	grantRoleToUsers(db, roleName, users)

	data.SetId(name)
	// return ReadRoleGrants(data, meta)
	return nil
}

func grantRoleToRoles(db *sql.DB, roleName string, roles []string) error {
	for _, role := range roles {
		err := grantRoleToRole(db, roleName, role)
		if err != nil {
			return err
		}
	}
	return nil
}

func grantRoleToRole(db *sql.DB, role1, role2 string) error {
	g := snowflake.RoleGrant(role1)
	_, err := db.Exec(g.Role(role2).Statement())
	return err
}

func grantRoleToUsers(db *sql.DB, roleName string, users []string) error {
	for _, user := range users {
		err := grantRoleToUser(db, roleName, user)
		if err != nil {
			return err
		}
	}
	return nil
}

func grantRoleToUser(db *sql.DB, role1, user string) error {
	g := snowflake.RoleGrant(role1)
	_, err := db.Exec(g.User(user).Statement())
	return err
}

type grant struct {
	CreatedOn   sql.RawBytes   `db:"created_on"`
	Role        sql.NullString `db:"role"`
	GrantedTo   sql.NullString `db:"granted_to"`
	GranteeName sql.NullString `db:"grantee_name"`
	Grantedby   sql.NullString `db:"granted_by"`
}

func ReadRoleGrants(data *schema.ResourceData, meta interface{}) error {
	db := meta.(*sql.DB)
	roleName := data.Get("role_name").(string)

	roles := make([]string, 0)
	users := make([]string, 0)

	grants, err := readGrants(db, roleName)
	if err != nil {
		return err
	}

	for _, grant := range grants {
		switch grant.GrantedTo.String {
		case "ROLE":
			roles = append(roles, grant.GranteeName.String)
		case "USER":
			users = append(users, grant.GranteeName.String)
		default:
			return fmt.Errorf("unknown grant type %s", grant.GrantedTo.String)
		}
	}

	data.Set("roles", roles)
	data.Set("users", users)

	return nil
}

func readGrants(db *sql.DB, roleName string) ([]*grant, error) {
	sdb := sqlx.NewDb(db, "snowflake")

	rows, err := sdb.Queryx(fmt.Sprintf("SHOW GRANTS OF ROLE %s", roleName))

	if err != nil {
		return nil, err
	}

	grants := make([]*grant, 0)
	for rows.Next() {
		g := &grant{}
		err = rows.StructScan(g)
		if err != nil {
			return nil, err
		}
		log.Printf("[DEBUG] row #%v", g)
		grants = append(grants, g)

	}
	return grants, nil
}

func DeleteRoleGrants(data *schema.ResourceData, meta interface{}) error {

	return nil
}

func UpdateRoleGrants(data *schema.ResourceData, meta interface{}) error {

	return ReadRoleGrants(data, meta)
}

// borrowed from https://github.com/terraform-providers/terraform-provider-aws/blob/master/aws/structure.go#L924:6
func expandStringList(configured []interface{}) []string {
	vs := make([]string, 0, len(configured))
	for _, v := range configured {
		val, ok := v.(string)
		if ok && val != "" {
			vs = append(vs, v.(string))
		}
	}
	return vs
}
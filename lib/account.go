package readraptor

import (
	"crypto/sha1"
	"fmt"
	"os"
	"time"

	"github.com/coopernurse/gorp"
	"github.com/cupcake/gokiq"
	"github.com/technoweenie/grohl"
)

type Account struct {
	Id         int64     `db:"id"          json:"id"`
	Created    time.Time `db:"created_at"  json:"created"`
	Email      string    `db:"email"       json:"email"`
	PublicKey  string    `db:"public_key"  json:"publicKey"`
	PrivateKey string    `db:"private_key" json:"privateKey"`

	// Confirmable
	ConfirmationToken  *string    `db:"confirmation_token"   json:"-"`
	ConfirmationSentAt *time.Time `db:"confirmation_sent_at" json:"-"`
	ConfirmedAt        *string    `db:"confirmed_at"         json:"-"`
}

func NewAccount(email string) *Account {
	return &Account{
		Created:    time.Now(),
		Email:      email,
		PublicKey:  genKey("public" + email),
		PrivateKey: genKey("private" + email),
	}
}

func FindAccount(id int64) (*Account, error) {
	var account Account
	err := dbmap.SelectOne(&account, "select * from accounts where id = $1", id)
	return &account, err
}

func FindAccountByPublicKey(dbmap *gorp.DbMap, key string) (*Account, error) {
	var account Account
	err := dbmap.SelectOne(&account, "select * from accounts where public_key = $1", key)
	return &account, err
}

func (a *Account) SendNewAccountEmail(client *gokiq.ClientConfig) {
	client.QueueJob(&NewAccountEmailJob{
		AccountId: a.Id,
	})

	grohl.Log(grohl.Data{
		"queue":   "NewAccountEmailJob",
		"account": a.Id,
	})
}

func genKey(input string) string {
	salt := os.Getenv("API_GEN_SECRET") + fmt.Sprintf("%d", time.Now().UnixNano())
	hasher := sha1.New()
	hasher.Write([]byte(salt + input))
	return fmt.Sprintf("%x", hasher.Sum(nil))
}

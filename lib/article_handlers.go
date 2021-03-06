package readraptor

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cupcake/gokiq"
	"github.com/go-martini/martini"
	"github.com/lib/pq"
	"github.com/technoweenie/grohl"
)

type ArticleParams struct {
	Key        string           `json:"key"`
	Recipients []string         `json:"recipients"`
	Callbacks  []CallbackParams `json:"via"`
}

type CallbackParams struct {
	At         int64    `json:"at"`
	Recipients []string `json:"recipients"`
	Url        string   `json:"url"`
}

func GetArticles(params martini.Params, w http.ResponseWriter) (string, int) {
	var a Article
	err := dbmap.SelectOne(&a, "select * from articles where key = $1", params["_1"])
	a.AddReadReceipts(dbmap)

	if err != nil && err != sql.ErrNoRows {
		panic(err)
	}

	json, err := json.Marshal(a)
	if err != nil {
		panic(err)
	}

	w.Header().Set("Content-Type", "application/json")
	return string(json), http.StatusOK
}

// TODO: (whatupdave) this is currently unsecured to allow reading from javascript
// We should implement a secure key mechanism
func GetReaderArticles(req *http.Request, w http.ResponseWriter, params martini.Params) (string, int) {
	keys := req.URL.Query()["key"]
	if len(keys) == 0 {
		keys = req.URL.Query()["key[]"]
	}

	articles := make([]ArticleResponse, 0)

	if len(keys) > 0 {
		readerId, err := dbmap.SelectInt(`
				select id
				from readers
				where distinct_id = $1;`, params["distinct_id"])

		readerQuery := fmt.Sprintf(`
				select articles.key,
          articles.created_at,
					articles.updated_at,
					first_read_at,
					last_read_at,
					total_read_count,
					unique_read_count
				from articles
				  inner join read_receipts on read_receipts.article_id = articles.id
					  and read_receipts.reader_id = %d
				where key in $1`, readerId)

		query, args := GenerateInQuery(readerQuery, keys)
		_, err = dbmap.Select(&articles, query, args...)
		if err != nil {
			panic(err)
		}
	}

	json, err := json.Marshal(articles)
	if err != nil {
		panic(err)
	}

	w.Header().Set("Content-Type", "application/json")
	return string(json), http.StatusOK
}

func GetReaderArticlesAll(req *http.Request, w http.ResponseWriter, account *Account, params martini.Params) (string, int) {
	qs := req.URL.Query()
	pageS, perS := qs.Get("page"), qs.Get("per")
	page, _ := strconv.Atoi(pageS)
	limit, _ := strconv.Atoi(perS)

	if limit == 0 {
		limit = 25
	}
	offset := page * limit
	var articles []ArticleResponse
	_, err := dbmap.Select(&articles, `
		select articles.created_at,
		       articles.updated_at,
					 key,
   				 total_read_count,
					 first_read_at,
					 last_read_at
		from articles
		  inner join read_receipts on read_receipts.article_id = articles.id
			inner join readers on read_receipts.reader_id = readers.id
		where articles.account_id = $1
		  and readers.account_id = $1
			and readers.distinct_id = $2
		order by articles.updated_at desc
		limit $3 offset $4`, account.Id, params["distinct_id"], limit, offset)
	if err != nil {
		panic(err)
	}

	json, err := json.Marshal(articles)
	if err != nil {
		panic(err)
	}

	w.Header().Set("Content-Type", "application/json")
	return string(json), http.StatusOK
}

func PostArticles(client *gokiq.ClientConfig, req *http.Request, w http.ResponseWriter, account *Account) (string, int) {
	decoder := json.NewDecoder(req.Body)
	var p ArticleParams
	err := decoder.Decode(&p)
	if err != nil {
		panic(err)
	}

	cid, err := UpsertArticle(dbmap, account.Id, p.Key)
	if _, ok := err.(*pq.Error); ok {
		if strings.Index(err.Error(), `duplicate key value violates unique constraint "articles_key_key"`) == -1 {
			panic(err)
		}
	}

	grohl.Log(grohl.Data{
		"account":  account.Id,
		"register": p.Key,
		"readers":  p.Recipients,
	})

	rids, err := AddArticleReaders(dbmap, account.Id, cid, p.Recipients)
	for _, callback := range p.Callbacks {
		at := time.Unix(callback.At, 0)

		if callback.Recipients != nil {
			rids, err = AddArticleReaders(dbmap, account.Id, cid, callback.Recipients)
			if err != nil {
				panic(err)
			}
		}
		ScheduleCallbacks(client, rids, at, callback.Url)
	}

	ci, err := FindArticleWithReadReceipts(dbmap, cid)

	json, err := json.Marshal(map[string]interface{}{
		"article": ci,
	})
	if err != nil {
		panic(err)
	}

	go func() {
		hub.broadcast <- &Broadcast{rids, json}
	}()

	w.Header().Set("Content-Type", "application/json")
	return string(json), http.StatusCreated
}

func GenerateInQuery(query string, args []string) (string, []interface{}) {
	clauseArgs := make([]string, len(args))
	iArgs := make([]interface{}, len(args))

	for i, v := range args {
		iArgs[i] = interface{}(v)
		clauseArgs[i] = "$" + strconv.Itoa(i+1)
	}

	clause := "(" + strings.Join(clauseArgs, ",") + ")"

	return strings.Replace(query, "$1", clause, 1), iArgs
}

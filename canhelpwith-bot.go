package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/McKael/madon"
	"github.com/blevesearch/bleve"
	"github.com/gorilla/mux"
)

var index bleve.Index
var client *madon.Client

type config struct {
	AppName        string
	Webpage        string
	Permissions    []string
	Instance       string
	AppID          string
	AppSecret      string
	Username       string
	Password       string
	Hashtags       []string
	HashtagScanned map[string]int64
	Adress         string
}

type Status struct {
	ID      string
	Name    string
	Message template.HTML
	Score   float64
	URL     string
}

func (s *Status) Type() string {
	return "Status"
}

func loadConfig(path string) (config, error) {
	var c config
	var filename string
	if path != "" {
		filename = path
	} else {
		filename = "config.json"
	}
	body, err := ioutil.ReadFile(filename)
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(body, &c)

	return c, err

}

func saveConfig(c config, path string) error {
	var filename string
	if path != "" {
		filename = path
	} else {
		filename = "config.json"
	}
	j, err := json.Marshal(&c)
	if err != nil {
		return err
	}
	err = ioutil.WriteFile(filename, j, 755)
	return err
}

func addStatitoIndex(c config, client *madon.Client) {
	var stati []madon.Status
	for _, h := range c.Hashtags {
		var lp madon.LimitParams
		last := c.HashtagScanned[h]
		if last != 0 {
			lp.All = false
			lp.SinceID = last
		} else {
			lp.All = true
		}
		st, err := client.GetTimelines(h, false, false, &lp)
		if err != nil {
			log.Println("Couldnt load Stati:", err)
		} else {
			stati = append(stati, st...)
		}
		if len(st) > 0 {
			c.HashtagScanned[h] = st[0].ID
		}
		err = saveConfig(c, "")
		if err != nil {
			fmt.Println("Error saving config:", err)
		}
	}
	for _, s := range stati {
		var st Status
		st.ID = strconv.FormatInt(s.ID, 10)
		st.Name = s.Account.DisplayName + " / " + s.Account.Username
		st.URL = s.URL
		st.Message = template.HTML(s.Content)
		index.Index(st.ID, s)
	}

}

func showtemplate(w http.ResponseWriter, path string, data interface{}) {
	t, err := template.ParseFiles(path)
	if err != nil {
		fmt.Fprintln(w, "Error parsing template:", err)
		return
	}
	err = t.Execute(w, data)
	if err != nil {
		fmt.Fprintln(w, "Error executing template:", err)
		return
	}
}

func resultHandler(w http.ResponseWriter, r *http.Request) {
	q := r.FormValue("query")
	query := bleve.NewQueryStringQuery(q)
	searchRequest := bleve.NewSearchRequest(query)
	searchRequest.Highlight = bleve.NewHighlight()
	searchResult, err := index.Search(searchRequest)
	if err != nil {
		fmt.Fprintln(w, "Error executing search:", err)
	}
	searchRequest.SortBy([]string{"-_score"})
	var stati []Status
	for i, res := range searchResult.Hits {
		//var st status
		/*id, err := strconv.ParseInt(res.ID, 10, 64)
		if err != nil {
			fmt.Println(w, "Error parsing Integer:", err)
			return
		}
		s, err := client.GetStatus(id)
		if err != nil {
			fmt.Println(w, "Error getting Status:", err)
			return
		}
		st.Name = s.Account.DisplayName + " / " + s.Account.Username
		st.URL = s.URL
		st.Message = template.HTML(s.Content)
		st.Score = res.Score
		stati = append(stati, st)*/
		fmt.Println(i, res)
		for k, v := range res.Fields {
			fmt.Printf("Field %v. Value %v.\n", k, v)
		}
	}
	showtemplate(w, "templates/result.html", stati)
}

func frontendHandler(w http.ResponseWriter, r *http.Request) {
	a := r.FormValue("action")
	switch a {
	case "search":
		resultHandler(w, r)
	default:
		showtemplate(w, "templates/search.html", nil)
	}
}

func main() {
	var err error

	log.Println("Loading Config")
	c, err := loadConfig("")
	if err != nil {
		log.Fatal("Error loading Config:", err)
	}

	_, err = os.Stat("toot.bleve")
	if os.IsNotExist(err) {
		log.Println("Creating Search Index")
		statusMapping := bleve.NewDocumentMapping()
		messageFieldMapping := bleve.NewTextFieldMapping()
		messageFieldMapping.Analyzer = "en"
		statusMapping.AddFieldMappingsAt("Message", messageFieldMapping)
		nameFieldMapping := bleve.NewTextFieldMapping()
		nameFieldMapping.Analyzer = "en"
		statusMapping.AddFieldMappingsAt("Name", nameFieldMapping)
		urlFieldMapping := bleve.NewTextFieldMapping()
		urlFieldMapping.Index = false
		statusMapping.AddFieldMappingsAt("URL", urlFieldMapping)
		mapping := bleve.NewIndexMapping()
		mapping.AddDocumentMapping("Status", statusMapping)
		index, err = bleve.New("toot.bleve", mapping)
		if err != nil {
			log.Fatal("Error creating Index:", err)
		}
		c.HashtagScanned = make(map[string]int64)
	} else {
		if err == nil {
			index, err = bleve.Open("toot.bleve")
			if err != nil {
				log.Fatal("Error loading Index:", err)
			}
		} else {
			log.Fatal("Error checking for Index:", err)
		}
	}

	if (c.AppID != "") && (c.AppSecret != "") {
		client, err = madon.NewApp(c.AppName, c.Webpage, c.Permissions, madon.NoRedirect, c.Instance)
		if err != nil {
			log.Fatal("Error creating App on Instance:", err)
		}
		c.AppID = client.ID
		c.AppSecret = client.Secret
	} else {

		client, err = madon.RestoreApp(c.AppName, c.Instance, c.AppID, c.AppSecret, nil)
		if err != nil {
			log.Fatal("Error creating App on Instance:", err)
		}
	}
	log.Println("Created App. Logging in now.")
	err = client.LoginBasic(c.Username, c.Password, c.Permissions)
	if err != nil {
		log.Fatal("Failed to log into accout:", err)
	}

	log.Println("Performing initial Scan")
	addStatitoIndex(c, client)

	log.Println("Starting Scan Timer")
	ticker := time.NewTicker(1 * time.Minute)
	go func(c config, client *madon.Client) {
		for range ticker.C {
			addStatitoIndex(c, client)
		}
	}(c, client)

	r := mux.NewRouter()
	r.HandleFunc("/", frontendHandler)
	log.Fatal(http.ListenAndServe(c.Adress, r))
}

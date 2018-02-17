package main

import (
	"bytes"
	"database/sql"
	"flag"
	_ "github.com/mattn/go-sqlite3"
	"github.com/wirepair/autogcd"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"regexp"
	"time"
)

var (
	url                 string
	dbLocation          string
	htmlToReturn        string
	chromeHost          string
	willUpdatePrerender bool
	startupFlags        = []string{"--disable-new-tab-first-run", "--no-first-run", "--disable-translate"}
	debug               bool
	waitForTimeout      = time.Second * 5
	waitForRate         = time.Millisecond * 25
	navigationTimeout   = time.Second * 10
	stableAfter         = time.Millisecond * 450
	stabilityTimeout    = time.Second * 10
)

func init() {
	flag.StringVar(&url, "url", "/", "Url to parse")
	flag.StringVar(&dbLocation, "dbLocation", "./prerender.db", "Database location")
}

func main() {
	flag.Parse()
	http.HandleFunc("/", handler)
	http.ListenAndServe(":9333", nil)
}

func handler(w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	checkError(err)

	allowedHosts, hasAllowedHosts := os.LookupEnv("ALLOWED_HOSTS")

	// Check host and path and validate
	host := r.FormValue("host")
	path := r.FormValue("path")

	chromeHost, haschromeHost := os.LookupEnv("CHROME_HOST")
	if !haschromeHost {
		chromeHost = "localhost"
	}

	if !hasAllowedHosts {
		log.Printf("Does not have allowed_hosts %s", allowedHosts)
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	chromePort, haschromePort := os.LookupEnv("CHROME_PORT")
	if !haschromePort {
		chromePort = "9222"
	}

	regex, _ := regexp.Compile(allowedHosts)
	found := regex.FindStringSubmatchIndex(host)

	if len(found) == 0 {
		log.Printf("Did not find host %s in %s ", host, allowedHosts)
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	log.Printf("Found host %s in %s ", host, allowedHosts)
	htmlToReturn = parseUrl(host, chromeHost, chromePort, path)
	w.Write([]byte(htmlToReturn))
}

func parseUrl(host string, chromeHost string, chromePort string, path string) (htmlToReturn string) {
	var buffer bytes.Buffer
	buffer.WriteString("https://")
	buffer.WriteString(host)
	buffer.WriteString(path)
	url = buffer.String()
	log.Printf("Will parse %s", url)

	db := startDb(dbLocation)
	defer db.Close()

	entryExists, entryId, entryHTML, entryUpdatedDate := checkForExistingPrerender(db, url)
	willUpdatePrerender = false

	if entryExists == false {
		willUpdatePrerender = true
	} else {
		options, err := http.Head(url)
		checkError(err)
		options.Body.Close()
		lastModifiedHeader := options.Header["Last-Modified"][0]
		lastModifiedDate, _ := time.Parse(time.RFC1123, lastModifiedHeader)
		lastModifiedStamp := lastModifiedDate.Unix()
		if lastModifiedStamp > entryUpdatedDate {
			_, err := db.Exec(`delete from prerender where id = ?`, entryId)
			checkError(err)
			willUpdatePrerender = true
		}
	}

	if willUpdatePrerender {
		htmlToReturn = fetchPrerender(chromeHost, chromePort, url)
		_, dbexecerr := db.Exec(`insert into prerender(url, html, updated) values(?, ?, strftime('%s', 'now'))`, url, htmlToReturn)
		checkError(dbexecerr)
		log.Printf("Done adding to db")
	} else {
		htmlToReturn = entryHTML
	}

	return htmlToReturn
}
func startDb(dbLocation string) (db *sql.DB) {
	db, err := sql.Open("sqlite3", dbLocation)
	checkError(err)

	sqlStmt := `create table if not exists prerender (id integer not null primary key, url text, updated text, html text)`
	_, sqlStmtErr := db.Exec(sqlStmt)
	checkError(sqlStmtErr)

	return db
}

func checkForExistingPrerender(db *sql.DB, url string) (exists bool, id int, html string, updated int64) {
	exists = false
	rows, err := db.Query(`select * from prerender where url = ? limit 1`, url)
	checkError(err)
	defer rows.Close()

	for rows.Next() {
		err = rows.Scan(&id, &url, &updated, &html)
		checkError(err)
	}

	err = rows.Err()
	checkError(err)

	if id > 0 {
		exists = true
	}

	return exists, id, html, updated
}

func fetchPrerender(chromeHost string, chromePort string, url string) (html string) {
	settings := autogcd.NewSettings("", randUserDir())
	settings.SetInstance(chromeHost, chromePort)
	settings.RemoveUserDir(true)           // clean up user directory after exit
	settings.AddStartupFlags(startupFlags) // disable new tab junk
	auto := autogcd.NewAutoGcd(settings)
	err := auto.Start()
	checkError(err)

	tab, newtaberr := auto.NewTab()
	checkError(newtaberr)
	tab, gettaberr := auto.GetTab() // get the first visual tab
	checkError(gettaberr)

	activatetaberr := auto.ActivateTab(tab)
	checkError(activatetaberr)
	configureTab(tab)

	_, errText, err := tab.Navigate(url)
	if err != nil {
		log.Printf("Navigation failed %s.", errText)
		checkError(err)
	}

	tab.SetStabilityTimeout(stabilityTimeout)
	waitstableerr := tab.WaitStable()
	checkError(waitstableerr)

	nodeId := tab.GetTopNodeId()
	pageSource, pagesourceerr := tab.GetPageSource(nodeId)
	checkError(pagesourceerr)
	// closetaberr := auto.CloseTab(tab)
	// checkError(closetaberr)

	// _, refreshTabsErr := auto.RefreshTabList()
	// checkError(refreshTabsErr)
	shutdownErr := auto.Shutdown()
	checkError(shutdownErr)

	return pageSource
}

func configureTab(tab *autogcd.Tab) {
	tab.SetNavigationTimeout(navigationTimeout) // give up after 10 seconds for navigating, default is 30 seconds
	tab.SetStabilityTime(stableAfter)
	if debug {
		domHandlerFn := func(tab *autogcd.Tab, change *autogcd.NodeChangeEvent) {
			log.Printf("change event %s occurred\n", change.EventType)
		}
		tab.GetDOMChanges(domHandlerFn)
	}
}

func randUserDir() string {
	dir, err := ioutil.TempDir("/tmp", "autogcd")
	checkError(err)
	return dir
}

func checkError(err error) {
	if err != nil {
		log.Panic(err)
	}
}

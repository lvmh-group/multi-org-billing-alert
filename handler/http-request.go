package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"gblaquiere.dev/multi-org-billing-alert/internal"
	"gblaquiere.dev/multi-org-billing-alert/internal/httperrors"
	"gblaquiere.dev/multi-org-billing-alert/model"
	"github.com/gorilla/mux"
	"io/ioutil"
	"log"
	"net/http"
	"regexp"
	"strings"
)

/*
	Received the JSON message of Alert Creation in HTTP Request
*/

var projectIdParam = "projectid"
var alertNameParam = "alertname"
var reBearer, _ = regexp.Compile(`^\s*Bearer\s+`)

func DeleteBudgetAlert(w http.ResponseWriter, r *http.Request) {

	name, err := getAlertName(r)

	if err != nil {
		http.Error(w, err.Error(), httperrors.GetHttpCode(err))
		return
	}

	billingAlert, err := internal.DeleteBillingAlert(r.Context(), name)
	if err != nil {
		log.Printf("internal.DeleteBillingAlert: %v\n", err)
		http.Error(w, err.Error(), httperrors.GetHttpCode(err))
		return
	}

	w.Header().Add("Content-type", "application/json")
	w.WriteHeader(http.StatusOK)
	formatResponse(w, billingAlert)
}

func GetBudgetAlert(w http.ResponseWriter, r *http.Request) {

	name, err := getAlertName(r)

	if err != nil {
		http.Error(w, err.Error(), httperrors.GetHttpCode(err))
		return
	}

	billingAlert, err := internal.GetBillingAlert(r.Context(), name)
	if err != nil {
		log.Printf("internal.GetBillingAlert: %v\n", err)
		http.Error(w, err.Error(), httperrors.GetHttpCode(err))
		return
	}

	w.WriteHeader(http.StatusOK)
	formatResponse(w, billingAlert)
}

func getAlertName(r *http.Request) (name string, err error) {
	vars := mux.Vars(r)
	projectId := vars[projectIdParam]
	alertName := vars[alertNameParam]

	if projectId != "" && alertName != "" && projectId != alertName {
		return "", httperrors.New(errors.New("projectID and AlertName provided and different"), http.StatusBadRequest)
	}

	name = projectId
	if alertName != "" {
		name = alertName
	}
	return
}
func GetUserMail(r *http.Request) (email string, err error) {
	authHeaders, exists := r.Header["Authorization"]
	if exists == false {
		email = ""
		return
	}
	var token string = ""
	for _, b := range authHeaders {
		matched := reBearer.MatchString(b)
		if matched {
			token = reBearer.ReplaceAllString(b, "")
		}
	}
	if token == "" {
		email = ""
		return
	}
	res, err := http.Get("https://oauth2.googleapis.com/tokeninfo?id_token=" + token)
	if err != nil {
		email = ""
		return
	}

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		email = ""
		return
	}
	var data model.InfoToken
	//var data map[string]any
	json.Unmarshal(body, &data)
	email = data.Email
	return
}
func RestGetBudgetAlert(w http.ResponseWriter, r *http.Request) {
	qsId, exists := r.URL.Query()["id"]
	var names []string
	if exists {
		names = strings.Split(qsId[0], ",")
	} else {
		vars := mux.Vars(r)
		projectIds := vars[projectIdParam]
		if projectIds != "" {
			names = strings.Split(projectIds, ",")
		}
	}
	userEmail, err := GetUserMail(r)
	if err != nil || userEmail == "" {
		resp := &model.Error{
			Error: "Auth error",
			Help:  "Unsure you are the  header 'Authorization: Bearer $(gcloud auth print-identity-token)' in your request ",
		}
		ErrorJson, _ := json.Marshal(resp)
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, string(ErrorJson))
		return
	}
	w.Header().Add("Content-type", "application/json")
	if len(names) == 1 && len(names[0]) == 0 || len(names) == 0 {
		resp := &model.Error{
			ProjectID: "not provided",
			Error:     "ProjectID is empty or not present",
			Help:      "format is : /http/projects/project1,project2 or /http/projects?id=project1,project2",
		}
		ErrorJson, _ := json.Marshal(resp)
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, string(ErrorJson))
		return
	}
	billingAlerts, billingAlertErrors, err := internal.RestGetBillingAlert(r.Context(), names, userEmail)
	if err != nil {
		log.Printf("internal.GetBillingAlert: %v\n", err)
		http.Error(w, err.Error(), httperrors.GetHttpCode(err))
		return
	}
	allInOne := []interface{}{}
	for _, allerte := range billingAlerts {
		allInOne = append(allInOne, allerte)
	}
	for _, allerte := range billingAlertErrors {
		allInOne = append(allInOne, allerte)
	}
	w.WriteHeader(http.StatusOK)
	output, _ := json.Marshal(allInOne)
	fmt.Fprint(w, string(output))

}

func UpsertBudgetAlert(w http.ResponseWriter, r *http.Request) {

	var billing model.BillingAlert
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Printf("ioutil.ReadAll: %v\n", err)
		http.Error(w, fmt.Sprintf("Bad Request %q", err), http.StatusBadRequest)
		return
	}
	log.Printf("Message content:\n %s\n", string(body))

	if err := json.Unmarshal(body, &billing); err != nil {
		log.Printf("json.Unmarshal: %v\n", err)
		http.Error(w, fmt.Sprintf("Bad Request %q", err), http.StatusBadRequest)
		return
	}

	billingAlert, err := internal.CreateBillingAlert(r.Context(), &billing)
	if err != nil {
		log.Printf("internal.CreateBillingAlert: %v\n", err)
		http.Error(w, err.Error(), httperrors.GetHttpCode(err))
		return
	}
	w.WriteHeader(http.StatusCreated)
	formatResponse(w, billingAlert)
}

func formatResponse(w http.ResponseWriter, billingAlert *model.BillingAlert) {
	billingAlertJson, _ := json.Marshal(billingAlert)
	fmt.Fprint(w, string(billingAlertJson))
	w.Header().Add("Content-type", "application/json")
}

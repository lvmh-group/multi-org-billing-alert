package billingAlertApi

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	budgetApi "cloud.google.com/go/billing/budgets/apiv1"
	budgetModel "cloud.google.com/go/billing/budgets/apiv1/budgetspb"
	"gblaquiere.dev/multi-org-billing-alert/internal/httperrors"
	"gblaquiere.dev/multi-org-billing-alert/internal/notificationChannelApi"
	"gblaquiere.dev/multi-org-billing-alert/model"
	billing_state "gblaquiere.dev/multi-org-billing-alert/model/billing-state"
	"google.golang.org/api/cloudresourcemanager/v1"
	ressourceManager "google.golang.org/api/cloudresourcemanager/v3"
	"google.golang.org/api/iterator"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

const projectPrefix = "projects/"

var defaultThresholds = []*budgetModel.ThresholdRule{ //predefined threshold
	{
		ThresholdPercent: 0.5,
	},
	{
		ThresholdPercent: 0.9,
	},
	{
		ThresholdPercent: 1.0,
	},
}

var client *budgetApi.BudgetClient = nil
var clientResourceManager *ressourceManager.Service = nil

//Initialize the client at startup
func init() {
	var err error
	ctx := context.Background()
	client, err = budgetApi.NewBudgetClient(ctx)
	if err != nil {
		log.Panicf("budgetApi.NewBudgetClient: %+v\n", err)
	}
	clientResourceManager, err = ressourceManager.NewService(ctx)
	if err != nil {
		log.Panicf("budgetApi.NewResourceManagerClient: %+v\n", err)
	}
}

func UpsertBillingAlert(ctx context.Context, message *model.BillingAlert) (billingAlert *model.BillingAlert, err error) {

	//Check if the budgetApi exists
	b, err := getExistingBillingAlert(ctx, getMessageName(message))

	if err != nil {
		return
	}

	// Create or update accordingly
	if b == nil {
		//Create a new budget
		b, err = createNewBudget(ctx, message)
		if err != nil {
			return
		}
		log.Printf("Budget creation successful for the project %s \n", message.ProjectID)
	} else {
		//Update the retrieved budget
		b, err = updateBudget(ctx, message, b)
		if err != nil {
			return
		}
		log.Printf("Budget update successful for the project %s \n", message.ProjectID)
	}
	billingAlert, err = createBillingAlertResponse(ctx, getMessageName(message), b)
	if err != nil {
		return
	}
	return
}

func getExistingBillingAlert(ctx context.Context, alertName string) (b *budgetModel.Budget, err error) {
	req := &budgetModel.ListBudgetsRequest{
		Parent: getBillingParent(),
	}
	budgets := client.ListBudgets(ctx, req)

	displayName := getDisplayName(alertName)
	for {
		budget, err := budgets.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Printf("budgets.Next: %+v\n", err)
			return nil, err
		}
		if budget.DisplayName == displayName {
			if b == nil {
				b = budget
			} else {
				err = errors.New("2 or more budget exists") //internal error, should not exist.
				log.Printf("impossible to get the budget, %+v, for this budget Name %s \n", err, displayName)
				return nil, err
			}
		}
	}
	return
}

func RestgetExistingBillingAlert(ctx context.Context, alertNames []string, userEmail string) (buffer []*budgetModel.Budget, bufferError []*model.Error, err error) {
	projects, deniedProjects := userProjects(alertNames, userEmail)
	req := &budgetModel.ListBudgetsRequest{
		Parent: getBillingParent(),
	}
	budgets := client.ListBudgets(ctx, req)

	for {
		budget, err := budgets.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			resp := &model.Error{
				Error: fmt.Sprintf("Internal Error budgets.Next: %+v", err),
			}
			log.Printf("budgets.Next: %+v\n", err)
			bufferError = append(bufferError, resp)
		}
		alerteName := strings.TrimPrefix(budget.DisplayName, "billing-")
		if stringInSlice(alerteName, projects) == true {
			buffer = append(buffer, budget)
			projects = popSlice(alerteName, projects)

		}
	}
	if len(projects) > 0 {
		for _, item := range projects {
			notFound := &model.Error{
				ProjectID: item,
				Error:     "Not Found",
			}
			bufferError = append(bufferError, notFound)
		}
	}
	for _, p := range deniedProjects {
		denied := &model.Error{
			ProjectID: p,
			Error:     "Access denied or not exist",
		}
		bufferError = append(bufferError, denied)
	}
	return
}

func getExistingMultiBillingAlert(ctx context.Context, alertName string) (b *budgetModel.Budget, err error) {
	req := &budgetModel.ListBudgetsRequest{
		Parent: getBillingParent(),
	}
	budgets := client.ListBudgets(ctx, req)

	displayName := getDisplayName(alertName)
	for {
		budget, err := budgets.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Printf("budgets.Next: %+v\n", err)
			return nil, err
		}
		if budget.DisplayName == displayName {
			if b == nil {
				b = budget
			} else {
				err = errors.New("2 or more budget exists") //internal error, should not exist.
				log.Printf("impossible to get the budget, %+v, for this budget Name %s \n", err, displayName)
				return nil, err
			}
		}
	}
	return
}

func updateBudget(ctx context.Context, message *model.BillingAlert, b *budgetModel.Budget) (budget *budgetModel.Budget, err error) {
	updatedPath := updateBudgetAlert(b, message)
	req := &budgetModel.UpdateBudgetRequest{
		Budget: b,
		UpdateMask: &fieldmaskpb.FieldMask{
			Paths: updatedPath,
		},
	}
	budget, err = client.UpdateBudget(ctx, req)
	if err != nil {
		log.Printf("client.UpdateBudget: %+v\n", err)
		return
	}
	return
}

func createNewBudget(ctx context.Context, message *model.BillingAlert) (budget *budgetModel.Budget, err error) {
	b := &budgetModel.Budget{ //Initiate a new budget alert object
		DisplayName:    getDisplayName(getMessageName(message)),
		ThresholdRules: defaultThresholds,
	}
	updateBudgetAlert(b, message)
	req := &budgetModel.CreateBudgetRequest{
		Parent: getBillingParent(),
		Budget: b,
	}
	budget, err = client.CreateBudget(ctx, req)
	if err != nil {
		log.Printf("client.CreateBudget: %+v\n", err)
		return
	}
	return
}

func getMessageName(message *model.BillingAlert) string {
	name := message.ProjectID
	if message.GroupAlert != nil {
		name = message.GroupAlert.AlertName
	}
	return name
}

//Merge all the project IDs from message and from the projectList in group alert
func getProjectList(message *model.BillingAlert) (projectList []string) {
	if message.ProjectID != "" {
		projectList = append(projectList, fmt.Sprintf("%s%s", projectPrefix, message.ProjectID))
	}
	if message.GroupAlert != nil && len(message.GroupAlert.ProjectIds) > 0 {
		for _, projectId := range message.GroupAlert.ProjectIds {
			projectList = append(projectList, fmt.Sprintf("%s%s", projectPrefix, projectId))
		}
	}
	return
}

func updateBudgetAlert(b *budgetModel.Budget, message *model.BillingAlert) (updatedPath []string) {
	b.Amount = &budgetModel.BudgetAmount{
		BudgetAmount: &budgetModel.BudgetAmount_SpecifiedAmount{
			SpecifiedAmount: &money.Money{
				CurrencyCode: "EUR",                                                                               //static currency
				Units:        int64(message.MonthlyBudget),                                                        //get only the int part
				Nanos:        int32((message.MonthlyBudget - float32(int32(message.MonthlyBudget))) * 1000000000), //remove the int part and set the floating part 10^9 to get a int
			},
		},
	}
	b.NotificationsRule = &budgetModel.NotificationsRule{
		MonitoringNotificationChannels: message.ChannelIds,
		DisableDefaultIamRecipients:    true, //to not disturb the Billing administrator
	}
	b.BudgetFilter = &budgetModel.Filter{
		Projects: getProjectList(message),
	}

	updatedPath = []string{ //Only these 2 fields to update. Can add more if required
		"amount.specified_amount",
		"notifications_rule.monitoring_notification_channels",
		"budget_filter.projects",
	}

	//Optional thresholds
	if len(message.Thresholds) > 0 {
		updatedPath = append(updatedPath, "threshold_rules")

		newThreshold := []*budgetModel.ThresholdRule{}
		for _, threshold := range message.Thresholds {
			newThreshold = append(newThreshold, &budgetModel.ThresholdRule{
				ThresholdPercent: threshold,
			})
		}
		b.ThresholdRules = newThreshold
	}

	return
}

func getDisplayName(alertName string) string {
	return fmt.Sprintf("billing-%s", alertName) //static naming
}

func getBillingParent() string {
	return fmt.Sprintf("billingAccounts/%s", os.Getenv("BILLING_ACCOUNT"))
}

func GetBillingAlert(ctx context.Context, alertName string) (billingAlert *model.BillingAlert, err error) {

	b, err := getExistingBillingAlert(ctx, alertName)
	if err != nil {
		return
	}

	if b == nil {
		err = httperrors.New(errors.New(fmt.Sprintf("projectid %q does not exist", alertName)), http.StatusNotFound)
		return
	}

	billingAlert, err = createBillingAlertResponse(ctx, alertName, b)
	return
}

func RestGetBillingAlert(ctx context.Context, alertNames []string, userEmail string) (billingAlerts []*model.BillingAlert, billingAlertsErrors []*model.Error, err error) {

	b, errors, err := RestgetExistingBillingAlert(ctx, alertNames, userEmail)
	if err != nil {
		return
	}
	billingAlertsErrors = errors

	billingAlerts, err = RestcreateBillingAlertResponse(ctx, alertNames, b)
	return
}

func createBillingAlertResponse(ctx context.Context, alertName string, budget *budgetModel.Budget) (billingAlert *model.BillingAlert, err error) {
	//Prepare response
	emails, err := getEmailList(ctx, budget)
	if err != nil {
		return
	}
	fmt.Println(len(emails))

	billingAlert = &model.BillingAlert{
		MonthlyBudget: float32(budget.Amount.GetSpecifiedAmount().Units) + (float32(budget.Amount.GetSpecifiedAmount().Nanos) / 1000000000),
		Emails:        emails,
		Thresholds:    getThresholds(budget),
	}

	projectList, err := getProjectIds(ctx, budget.BudgetFilter.Projects)
	if err != nil {
		return nil, err
	}

	if len(budget.BudgetFilter.GetProjects()) > 1 ||
		(len(budget.BudgetFilter.GetProjects()) == 1 && projectList[0] != alertName) {
		billingAlert.GroupAlert = &model.GroupAlert{
			ProjectIds: projectList,
			AlertName:  alertName,
		}
	} else {
		if len(projectList) == 0 {
			billingAlert.Warning = fmt.Sprintf("Project '%s': Not Found on GCP", alertName)
		} else {
			billingAlert.ProjectID = projectList[0]
		}
		billingAlert.GroupAlert = &model.GroupAlert{
			AlertName:  alertName,
			ProjectIds: projectList,
		}
	}
	return
}

func RestcreateBillingAlertResponse(ctx context.Context, alertNames []string, budgets []*budgetModel.Budget) (billingAlerts []*model.BillingAlert, err error) {
	//Prepare response
	for _, budget := range budgets {
		emails, errs := getEmailList(ctx, budget)
		if errs != nil {
			//
			//
			log.Printf("%+v\n", errs)
		}

		billingAlert := &model.BillingAlert{
			MonthlyBudget: float32(budget.Amount.GetSpecifiedAmount().Units) + (float32(budget.Amount.GetSpecifiedAmount().Nanos) / 1000000000),
			Emails:        emails,
			Thresholds:    getThresholds(budget),
		}

		projectList, err := getProjectIds(ctx, budget.BudgetFilter.Projects)
		if err != nil {
			log.Printf("Project not found on GCP\n")
		}
		name := strings.TrimPrefix(budget.DisplayName, "billing-")
		if len(budget.BudgetFilter.GetProjects()) > 1 ||
			(len(budget.BudgetFilter.GetProjects()) == 1 && stringInSlice(projectList[0], alertNames) == false) {
			billingAlert.GroupAlert = &model.GroupAlert{
				ProjectIds: projectList,
				AlertName:  name,
			}
		} else {
			if len(projectList) == 0 {
				billingAlert.Warning = fmt.Sprintf("Project '%s': Not Found on GCP", name)
			} else {
				billingAlert.ProjectID = projectList[0]
			}
			billingAlert.GroupAlert = &model.GroupAlert{
				AlertName:  name,
				ProjectIds: projectList,
			}
		}
		billingAlerts = append(billingAlerts, billingAlert)
	}
	return
}

func getProjectIds(ctx context.Context, projects []string) (projectList []string, err error) {
	for _, project := range projects {
		p, err := clientResourceManager.Projects.Get(project).Context(ctx).Do()
		if err != nil {
			log.Printf("resourceManager.GetProject: %+v; deleted?\n", err)
			projectList = append(projectList, fmt.Sprintf("%s (unknown)", project))
		} else {
			projectList = append(projectList, p.ProjectId)
		}
	}
	return
}

func getThresholds(b *budgetModel.Budget) (thresholds []float64) {
	for _, t := range b.ThresholdRules {
		thresholds = append(thresholds, t.ThresholdPercent)
	}
	return
}

func getEmailList(ctx context.Context, b *budgetModel.Budget) (emailList []string, err error) {
	for _, channelId := range b.NotificationsRule.MonitoringNotificationChannels {
		channel, err := notificationChannelApi.GetChannelID(ctx, channelId)
		if err != nil {
			return nil, httperrors.New(err, http.StatusInternalServerError)
		}
		email := channel.Labels[notificationChannelApi.EmailAddressLabelKey]
		emailList = append(emailList, email)
	}
	return
}

func DeleteBillingAlert(ctx context.Context, alertName string) (billingAlert *model.BillingAlert, err error) {
	b, err := getExistingBillingAlert(ctx, alertName)
	if err != nil {
		return nil, httperrors.New(err, http.StatusInternalServerError)
	}

	if b == nil {
		return nil, httperrors.New(errors.New(fmt.Sprintf("projectid %q does not exist", alertName)), http.StatusNotFound)
	}

	//Prepare the response
	billingAlert, err = createBillingAlertResponse(ctx, alertName, b)

	if err != nil {
		return nil, httperrors.New(err, http.StatusInternalServerError)
	}

	req := &budgetModel.DeleteBudgetRequest{
		Name: b.Name,
	}
	err = client.DeleteBudget(ctx, req)

	if err != nil {
		return nil, httperrors.New(err, http.StatusInternalServerError)
	}

	//Update state
	billingAlert.State = billing_state.Deleted

	return
}

func stringInSlice(a string, list []string) bool {
	for _, b := range list {
		if strings.Contains(b, a) {
			return true
		}
	}
	return false
}
func stringMatchInSlice(a string, list []string) string {
	for _, b := range list {
		if strings.Contains(b, a) {
			return b
		}
	}
	return ""
}

func popSlice(item string, slice []string) (ret []string) {
	for _, str := range slice {
		if str == item {
			continue
		}
		ret = append(ret, str)
	}
	return
}

func userProjects(requestedProjects []string, email string) (autorized []string, denied []string) {
	ctx := context.Background()
	for _, projectId := range requestedProjects {

		var role string = "roles/editor"

		ctx, cancel := context.WithTimeout(ctx, time.Second*10)
		crmService, err := cloudresourcemanager.NewService(ctx)
		defer cancel()
		request := new(cloudresourcemanager.GetIamPolicyRequest)
		policy, err := crmService.Projects.GetIamPolicy(projectId, request).Do()
		if err != nil {
			fmt.Printf("Projects.GetIamPolicy: %v\n", err)
			denied = append(denied, projectId)
			continue
		}

		var binding *cloudresourcemanager.Binding
		for _, b := range policy.Bindings {
			if b.Role == role {
				binding = b
				break
			}
		}
		if stringInSlice(email, binding.Members) == true {
			autorized = append(autorized, projectId)
		} else {
			denied = append(denied, projectId)
		}
	}
	fmt.Printf("%+v\n", denied)
	fmt.Printf("%+v\n", autorized)
	return
}

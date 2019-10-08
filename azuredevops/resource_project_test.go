package azuredevops

// The tests in this file use the mock clients in mock_client.go to mock out
// the Azure DevOps client operations.

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/microsoft/terraform-provider-azuredevops/azuredevops/utils/converter"

	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform/helper/acctest"
	"github.com/hashicorp/terraform/helper/resource"
	"github.com/hashicorp/terraform/helper/schema"
	"github.com/hashicorp/terraform/terraform"
	"github.com/microsoft/azure-devops-go-api/azuredevops/core"
	"github.com/microsoft/azure-devops-go-api/azuredevops/operations"
	"github.com/stretchr/testify/require"
)

var testID = uuid.New()
var testProject = core.TeamProject{
	Id:          &testID,
	Name:        converter.String("Name"),
	Visibility:  &core.ProjectVisibilityValues.Public,
	Description: converter.String("Description"),
	Capabilities: &map[string]map[string]string{
		"versioncontrol":  {"sourceControlType": "SouceControlType"},
		"processTemplate": {"templateTypeId": testID.String()},
	},
}

/**
 * Begin unit tests
 */

// verifies that the create operation is considered failed if the initial API
// call fails.
func TestAzureDevOpsProject_CreateProject_DoesNotSwallowErrorFromFailedCreateCall(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	coreClient := NewMockCoreClient(ctrl)
	clients := &aggregatedClient{
		CoreClient: coreClient,
		ctx:        context.Background(),
	}

	expectedProjectCreateArgs := core.QueueCreateProjectArgs{ProjectToCreate: &testProject}

	coreClient.
		EXPECT().
		QueueCreateProject(clients.ctx, expectedProjectCreateArgs).
		Return(nil, errors.New("QueueCreateProject() Failed")).
		Times(1)

	err := createProject(clients, &testProject, 5)
	require.Equal(t, "QueueCreateProject() Failed", err.Error())
}

// verifies that the create operation is considered failed if there is an issue
// verifying via the async polling operation API that it has completed successfully.
func TestAzureDevOpsProject_CreateProject_DoesNotSwallowErrorFromFailedAsyncStatusCheckCall(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	coreClient := NewMockCoreClient(ctrl)
	operationsClient := NewMockOperationsClient(ctrl)
	clients := &aggregatedClient{
		CoreClient:       coreClient,
		OperationsClient: operationsClient,
		ctx:              context.Background(),
	}

	expectedProjectCreateArgs := core.QueueCreateProjectArgs{ProjectToCreate: &testProject}
	mockedOperationReference := operations.OperationReference{Id: &testID}
	expectedOperationArgs := operations.GetOperationArgs{OperationId: &testID}

	coreClient.
		EXPECT().
		QueueCreateProject(clients.ctx, expectedProjectCreateArgs).
		Return(&mockedOperationReference, nil).
		Times(1)

	operationsClient.
		EXPECT().
		GetOperation(clients.ctx, expectedOperationArgs).
		Return(nil, errors.New("GetOperation() failed")).
		Times(1)

	err := createProject(clients, &testProject, 5)
	require.Equal(t, "GetOperation() failed", err.Error())
}

// verifies that polling is done to validate the status of the asynchronous
// testProject create operation.
func TestAzureDevOpsProject_CreateProject_PollsUntilOperationIsSuccessful(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	coreClient := NewMockCoreClient(ctrl)
	operationsClient := NewMockOperationsClient(ctrl)
	clients := &aggregatedClient{
		CoreClient:       coreClient,
		OperationsClient: operationsClient,
		ctx:              context.Background(),
	}

	expectedProjectCreateArgs := core.QueueCreateProjectArgs{ProjectToCreate: &testProject}
	mockedOperationReference := operations.OperationReference{Id: &testID}
	expectedOperationArgs := operations.GetOperationArgs{OperationId: &testID}

	coreClient.
		EXPECT().
		QueueCreateProject(clients.ctx, expectedProjectCreateArgs).
		Return(&mockedOperationReference, nil).
		Times(1)

	firstStatus := operationWithStatus(operations.OperationStatusValues.InProgress)
	firstPoll := operationsClient.
		EXPECT().
		GetOperation(clients.ctx, expectedOperationArgs).
		Return(&firstStatus, nil)

	secondStatus := operationWithStatus(operations.OperationStatusValues.Succeeded)
	secondPoll := operationsClient.
		EXPECT().
		GetOperation(clients.ctx, expectedOperationArgs).
		Return(&secondStatus, nil)

	gomock.InOrder(firstPoll, secondPoll)

	err := createProject(clients, &testProject, 5)
	require.Equal(t, nil, err)
}

// verifies that if a project takes too long to create, an error is returned
func TestAzureDevOpsProject_CreateProject_ReportsErrorIfNoSuccessForLongTime(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	coreClient := NewMockCoreClient(ctrl)
	operationsClient := NewMockOperationsClient(ctrl)
	clients := &aggregatedClient{
		CoreClient:       coreClient,
		OperationsClient: operationsClient,
		ctx:              context.Background(),
	}

	expectedProjectCreateArgs := core.QueueCreateProjectArgs{ProjectToCreate: &testProject}
	mockedOperationReference := operations.OperationReference{Id: &testID}
	expectedOperationArgs := operations.GetOperationArgs{OperationId: &testID}

	coreClient.
		EXPECT().
		QueueCreateProject(clients.ctx, expectedProjectCreateArgs).
		Return(&mockedOperationReference, nil).
		Times(1)

	// the operation will forever be "in progress"
	status := operationWithStatus(operations.OperationStatusValues.InProgress)
	operationsClient.
		EXPECT().
		GetOperation(clients.ctx, expectedOperationArgs).
		Return(&status, nil).
		MinTimes(1)

	err := createProject(clients, &testProject, 5)
	require.NotNil(t, err, "Expected error indicating timeout")
}

func TestAzureDevOpsProject_FlattenExpand_RoundTrip(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	coreClient := NewMockCoreClient(ctrl)
	clients := &aggregatedClient{
		CoreClient: coreClient,
		ctx:        context.Background(),
	}

	expectedProcesses := []core.Process{
		{
			Name: converter.String("TemplateName"),
			Id:   &testID,
		},
	}

	// mock the list of all process IDs. This is needed for the call to flattenProject()
	coreClient.
		EXPECT().
		GetProcesses(clients.ctx, core.GetProcessesArgs{}).
		Return(&expectedProcesses, nil).
		Times(1)

	// mock the lookup of a specific process. This is needed for the call to expandProject()
	coreClient.
		EXPECT().
		GetProcessById(clients.ctx, core.GetProcessByIdArgs{ProcessId: &testID}).
		Return(&expectedProcesses[0], nil).
		Times(1)

	resourceData := schema.TestResourceDataRaw(t, resourceProject().Schema, nil)
	err := flattenProject(clients, resourceData, &testProject)
	require.Nil(t, err)

	projectAfterRoundTrip, err := expandProject(clients, resourceData)
	require.Nil(t, err)
	require.Equal(t, testProject, *projectAfterRoundTrip)
}

// verifies that the project ID is used for reads if the ID is set
func TestAzureDevOpsProject_ProjectRead_UsesIdIfSet(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	coreClient := NewMockCoreClient(ctrl)
	clients := &aggregatedClient{
		CoreClient: coreClient,
		ctx:        context.Background(),
	}

	id := "id"
	name := "name"

	coreClient.
		EXPECT().
		GetProject(clients.ctx, core.GetProjectArgs{
			ProjectId:           &id,
			IncludeCapabilities: converter.Bool(true),
			IncludeHistory:      converter.Bool(false),
		}).
		Times(1)

	projectRead(clients, id, name)
}

// verifies that the project name is used for reads if the ID is not set
func TestAzureDevOpsProject_ProjectRead_UsesNameIfIdNotSet(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	coreClient := NewMockCoreClient(ctrl)
	clients := &aggregatedClient{
		CoreClient: coreClient,
		ctx:        context.Background(),
	}

	id := ""
	name := "name"

	coreClient.
		EXPECT().
		GetProject(clients.ctx, core.GetProjectArgs{
			ProjectId:           &name,
			IncludeCapabilities: converter.Bool(true),
			IncludeHistory:      converter.Bool(false),
		}).
		Times(1)

	projectRead(clients, id, name)
}

// creates an operation given a status
func operationWithStatus(status operations.OperationStatus) operations.Operation {
	return operations.Operation{Status: &status}
}

/**
 * Begin acceptance tests
 */

// Verifies that the following sequence of events occurrs without error:
//	(1) TF apply creates project
//	(2) TF state values are set
//	(3) project can be queried by ID and has expected name
// 	(4) TF destroy deletes project
//	(5) project can no longer be queried by ID
func TestAccAzureDevOpsProject_Create(t *testing.T) {
	projectName := testAccResourcePrefix + acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
	tfNode := "azuredevops_project.project"

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccProjectCheckDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccProjectResource(projectName),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet(tfNode, "process_template_id"),
					resource.TestCheckResourceAttr(tfNode, "project_name", projectName),
					resource.TestCheckResourceAttr(tfNode, "version_control", "Git"),
					resource.TestCheckResourceAttr(tfNode, "visibility", "private"),
					resource.TestCheckResourceAttr(tfNode, "work_item_template", "Agile"),
					testAccCheckProjectResourceExists(projectName),
				),
			},
		},
	})
}

// HCL describing an AzDO project
func testAccProjectResource(projectName string) string {
	return fmt.Sprintf(`
resource "azuredevops_project" "project" {
	project_name       = "%s"
	description        = "%s-description"
	visibility         = "private"
	version_control    = "Git"
	work_item_template = "Agile"
}`, projectName, projectName)
}

// Given the name of an AzDO project, this will return a function that will check whether
// or not the project (1) exists in the state and (2) exist in AzDO and (3) has the correct name
func testAccCheckProjectResourceExists(expectedName string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		resource, ok := s.RootModule().Resources["azuredevops_project.project"]
		if !ok {
			return fmt.Errorf("Did not find a project in the TF state")
		}

		clients := testAccProvider.Meta().(*aggregatedClient)
		id := resource.Primary.ID
		project, err := projectRead(clients, id, "")

		if err != nil {
			return fmt.Errorf("Project with ID=%s cannot be found!. Error=%v", id, err)
		}

		if *project.Name != expectedName {
			return fmt.Errorf("Project with ID=%s has Name=%s, but expected Name=%s", id, *project.Name, expectedName)
		}

		return nil
	}

}

// verifies that all projects referenced in the state are destroyed. This will be invoked
// *after* terrafform destroys the resource but *before* the state is wiped clean.
func testAccProjectCheckDestroy(s *terraform.State) error {
	clients := testAccProvider.Meta().(*aggregatedClient)

	// verify that every project referenced in the state does not exist in AzDO
	for _, resource := range s.RootModule().Resources {
		if resource.Type != "azuredevops_project" {
			continue
		}

		id := resource.Primary.ID

		// indicates the project still exists - this should fail the test
		if _, err := projectRead(clients, id, ""); err == nil {
			return fmt.Errorf("project with ID %s should not exist", id)
		}
	}

	return nil
}

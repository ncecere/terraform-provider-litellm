package litellm

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

const (
	endpointUserNew    = "/user/new"
	endpointUserInfo   = "/user/info"
	endpointUserUpdate = "/user/update"
	endpointUserDelete = "/user/delete"
)

func ResourceLiteLLMuser() *schema.Resource {
	return &schema.Resource{
		Create: resourceLiteLLMUserCreate,
		Read:   resourceLiteLLMUserRead,
		Update: resourceLiteLLMUserUpdate,
		Delete: resourceLiteLLMUserDelete,

		Schema: map[string]*schema.Schema{
			"user_id": {
				Type:     schema.TypeString,
				Required: true,
			},
			"user_email": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"user_alias": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"key_alias": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"user_role": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"max_budget": {
				Type:     schema.TypeFloat,
				Optional: true,
			},
			"models": {
				Type:     schema.TypeList,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
			"tpm_limit": {
				Type:     schema.TypeInt,
				Optional: true,
			},
			"rpm_limit": {
				Type:     schema.TypeInt,
				Optional: true,
			},
			"auto_create_key": {
				Type:     schema.TypeBool,
				Optional: true,
			},
		},
	}
}

func resourceLiteLLMUserCreate(d *schema.ResourceData, m interface{}) error {
	client := m.(*Client)

	userID := uuid.New().String()
	userData := buildUserData(d, userID)

	log.Printf("[DEBUG] Create user request payload: %+v", userData)

	resp, err := MakeRequest(client, "POST", endpointUserNew, userData)
	if err != nil {
		return fmt.Errorf("error creating user: %w", err)
	}
	defer resp.Body.Close()

	if err := handleResponse(resp, "creating user"); err != nil {
		return err
	}

	d.SetId(userID)
	log.Printf("[INFO] User created with ID: %s", userID)

	return resourceLiteLLMUserRead(d, m)
}

func resourceLiteLLMUserRead(d *schema.ResourceData, m interface{}) error {
	client := m.(*Client)

	log.Printf("[INFO] Reading user with ID: %s", d.Id())

	resp, err := MakeRequest(client, "GET", fmt.Sprintf("%s?user_id=%s", endpointUserInfo, d.Id()), nil)
	if err != nil {
		return fmt.Errorf("error reading user: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		log.Printf("[WARN] user with ID %s not found, removing from state", d.Id())
		d.SetId("")
		return nil
	}

	var userResp UserResponse
	if err := json.NewDecoder(resp.Body).Decode(&userResp); err != nil {
		return fmt.Errorf("error decoding user info response: %w", err)
	}

	// Update the state with values from the response or fall back to the data passed in during creation
	d.Set("user_alias", GetStringValue(userResp.userAlias, d.Get("user_alias").(string)))

	return nil
}
func resourceLiteLLMUserUpdate(d *schema.ResourceData, m interface{}) error {
	client := m.(*Client)

	userData := buildUserData(d, d.Id())
	log.Printf("[DEBUG] Update user request payload: %+v", userData)

	resp, err := MakeRequest(client, "POST", endpointUserUpdate, userData)
	if err != nil {
		return fmt.Errorf("error updating user: %w", err)
	}
	defer resp.Body.Close()

	if err := handleResponse(resp, "updating user"); err != nil {
		return err
	}

	log.Printf("[INFO] Successfully updated user with ID: %s", d.Id())
	return resourceLiteLLMUserRead(d, m)
}

func resourceLiteLLMUserDelete(d *schema.ResourceData, m interface{}) error {
	client := m.(*Client)

	log.Printf("[INFO] Deleting user with ID: %s", d.Id())

	deleteData := map[string]interface{}{
		"user_ids": []string{d.Id()},
	}

	resp, err := MakeRequest(client, "POST", endpointUserDelete, deleteData)
	if err != nil {
		return fmt.Errorf("error deleting user: %w", err)
	}
	defer resp.Body.Close()

	if err := handleResponse(resp, "deleting user"); err != nil {
		return err
	}

	log.Printf("[INFO] Successfully deleted user with ID: %s", d.Id())
	d.SetId("")
	return nil
}

func buildUserData(d *schema.ResourceData, userID string) map[string]interface{} {
	userData := map[string]interface{}{
		"user_id":    userID,
		"user_alias": d.Get("user_alias").(string),
	}

	for _, key := range []string{"user_email", "user_alias", "key_alias", "user_role", "max_budget", "models", "tpm_limit", "rpm_limit", "auto_create_keys"} {
		if v, ok := d.GetOk(key); ok {
			userData[key] = v
		}
	}

	return userData
}

use validator::Validate;

#[derive(Debug, Validate)]
pub struct SignupData {
    pub username: String,
    pub password: String,

    #[validate(must_match(
        other = "password",
        message = "Confirm password does not match password"
    ))]
    pub confirm_password: String,
}

impl SignupData {
    pub fn validate_signup(&self) -> Result<(), validator::ValidationErrors> {
        self.validate()
    }
}

# Seed the local dev server with a test user, project and tokens.
#
# Runs through `manage.py shell -c` inside the dev server's own checkout,
# only ever against its local SQLite database. Prints KEY=VALUE lines.
# Each run revokes the previous run's tokens and issues fresh ones.
from django.contrib.auth import get_user_model
from django.utils import timezone

from accounts.models import PersonalAPIToken
from billing.models import Plan
from devices.models import ReleaseChannel
from projects.models import Project, ProjectAPIToken

User = get_user_model()

user, created = User.objects.get_or_create(
    username="cli-dev", defaults={"email": "cli-dev@example.invalid"}
)
if created:
    user.set_unusable_password()
    user.save(update_fields=["password"])

profile = user.profile
profile.plan = Plan.objects.get(code="pro")
profile.email_verified = True
profile.save(update_fields=["plan", "email_verified", "updated_at"])

# A staff account with no projects of its own. The API lists projects for
# staff only when given an owner, so the CLI must log it in by project ID.
staff, created = User.objects.get_or_create(
    username="cli-dev-staff",
    defaults={"email": "cli-dev-staff@example.invalid", "is_staff": True},
)
if created:
    staff.set_unusable_password()
    staff.save(update_fields=["password"])

project, _ = Project.objects.get_or_create(
    owner=user,
    slug="cli-dev",
    defaults={"name": "CLI dev", "default_framework": "esp_idf"},
)
ReleaseChannel.objects.get_or_create(
    project=project, name="stable", defaults={"is_default": True}
)

now = timezone.now()
PersonalAPIToken.objects.filter(user__in=[user, staff], revoked_at__isnull=True).update(revoked_at=now)
project.api_tokens.filter(revoked_at__isnull=True).update(revoked_at=now)

_, pat = PersonalAPIToken.issue(user, "cli-dev")
_, staff_pat = PersonalAPIToken.issue(staff, "cli-dev-staff")
_, api_token = ProjectAPIToken.issue(project=project, name="cli-dev-ci", scopes=["api"])
_, device_token = ProjectAPIToken.issue(project=project, name="cli-dev-devices", scopes=["device"])

print(f"SIMPLEOTA_DEV_PROJECT_ID={project.id}")
print(f"SIMPLEOTA_DEV_PROJECT_SLUG={project.slug}")
print(f"SIMPLEOTA_DEV_PERSONAL_TOKEN={pat}")
print(f"SIMPLEOTA_DEV_STAFF_TOKEN={staff_pat}")
print(f"SIMPLEOTA_DEV_PROJECT_TOKEN={api_token}")
print(f"SIMPLEOTA_DEV_DEVICE_TOKEN={device_token}")

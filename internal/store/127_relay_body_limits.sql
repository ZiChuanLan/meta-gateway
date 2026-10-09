-- /v1 request-body ceilings as runtime settings.
--
-- Same story as 124 (outbound limits): the ceiling decides whether an ordinary
-- request — a chat call with inlined base64 screenshots is routinely over the
-- old hard-coded 10 MB — is refused, and a deployment's answer to "how big is
-- too big" changes long before its container does. Storing it here means the
-- operator raises it in the console and the next request uses it.
--
-- 0 = no override: the RELAY_MAX_BODY_MB / RELAY_MAX_IMAGE_MB bootstrap applies.
ALTER TABLE runtime_settings ADD COLUMN relay_max_body_mb INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runtime_settings ADD COLUMN relay_max_image_mb INTEGER NOT NULL DEFAULT 0;

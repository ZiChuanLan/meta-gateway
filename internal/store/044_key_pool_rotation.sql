-- Frontend-configurable key-pool rotation (hot reload). NULL = not overridden.
ALTER TABLE runtime_settings ADD COLUMN key_pool_rotation INTEGER;

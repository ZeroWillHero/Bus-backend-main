DROP POLICY IF EXISTS tickets_update ON booking.tickets;
DO $$
BEGIN
    IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'slbus_app') THEN
        BEGIN
            ALTER ROLE slbus_app RESET "app.current_operator_id";
            ALTER ROLE slbus_app RESET "app.current_user_id";
            ALTER ROLE slbus_app RESET "app.current_role";
        EXCEPTION WHEN insufficient_privilege THEN
            RAISE NOTICE 'skipping slbus_app role defaults reset (no privilege)';
        END;
    END IF;
END $$;
-- (grants on platform tables are left in place on down: harmless and needed by 000007's intent)

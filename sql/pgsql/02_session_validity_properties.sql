CREATE TYPE session_status AS ENUM('active', 'terminated');

ALTER TABLE sessions 
    ADD COLUMN status session_status DEFAULT 'active'
    ;

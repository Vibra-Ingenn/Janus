package validation

import "testing"

func TestValidateContentClean(t *testing.T) {
	clean := `def upload(path):
    with open(path, 'rb') as f:
        data = f.read()
    return data`
	if err := ValidateContent(clean); err != nil {
		t.Errorf("expected clean, got: %v", err)
	}
}

func TestValidateContentTODO(t *testing.T) {
	code := "def upload(path):\n    # TODO: add application logic here\n    pass\n"
	if err := ValidateContent(code); err == nil {
		t.Error("expected rejection for TODO comment")
	}
}

func TestValidateContentSlashTODO(t *testing.T) {
	code := "func Upload() {\n    // TODO implement\n}"
	if err := ValidateContent(code); err == nil {
		t.Error("expected rejection for // TODO")
	}
}

func TestValidateContentPass(t *testing.T) {
	code := "def handle():\n    pass\n"
	if err := ValidateContent(code); err == nil {
		t.Error("expected rejection for bare pass")
	}
}

func TestValidateContentNotImplemented(t *testing.T) {
	code := "def process():\n    raise NotImplementedError('not done')"
	if err := ValidateContent(code); err == nil {
		t.Error("expected rejection for NotImplementedError")
	}
}

func TestValidateContentHelpOnly(t *testing.T) {
	code := "python my_tool.py --help"
	if err := ValidateContent(code); err == nil {
		t.Error("expected rejection for --help only command")
	}
}

func TestValidateContentRealCode(t *testing.T) {
	code := `import dropbox
def upload_file(local_path, dropbox_folder):
    dbx = dropbox.Dropbox(token)
    with open(local_path, 'rb') as f:
        dbx.files_upload(f.read(), dropbox_folder + '/' + os.path.basename(local_path))
    return True`
	if err := ValidateContent(code); err != nil {
		t.Errorf("should not reject real code: %v", err)
	}
}

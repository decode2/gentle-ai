//go:build darwin && cgo && privatefileaclexperiment

package privatefile

/*
#include <sys/acl.h>
#include <sys/stat.h>
#include <fcntl.h>
#include <unistd.h>
#include <uuid/uuid.h>
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdarg.h>

// Experiment only: never used by WriteNewReport. The well-known Darwin UUID
// for everyone avoids resolving a local user/group as a substitute.
static void note(char *out, size_t n, const char *fmt, ...) {
    size_t used = strlen(out);
    if (used >= n) return;
    va_list ap;
    va_start(ap, fmt);
    vsnprintf(out + used, n - used, fmt, ap);
    va_end(ap);
}

#define REQUIRE(expr) do { if (!(expr)) { note(out,n,"FAIL %s errno=%d (%s)\n",#expr,errno,strerror(errno)); goto done; } } while (0)

// Prepare separately so Go can capture ls -le before the setter.
// Go retains and closes the same fd; neither phase reopens the child.
static int darwin_acl_prepare(const char *parent, const char *child, char *out, size_t n) {
    acl_t parent_acl = NULL;
    acl_entry_t entry;
    acl_permset_t perms;
    acl_flagset_t flags;
    uuid_t everyone;
    int fd = -1;
    out[0] = 0;
    REQUIRE(uuid_parse("FFFFEEEE-DDDD-CCCC-BBBB-AAAA0000000C", everyone) == 0);
    REQUIRE((parent_acl = acl_init(1)) != NULL);
    REQUIRE(acl_create_entry(&parent_acl, &entry) == 0);
    REQUIRE(acl_set_tag_type(entry, ACL_EXTENDED_ALLOW) == 0);
    REQUIRE(acl_set_qualifier(entry, &everyone) == 0);
    REQUIRE(acl_get_flagset_np(entry, &flags) == 0);
    REQUIRE(acl_add_flag_np(flags, ACL_ENTRY_FILE_INHERIT) == 0);
    REQUIRE(acl_get_permset(entry, &perms) == 0);
    REQUIRE(acl_add_perm(perms, ACL_READ_DATA) == 0);
    REQUIRE(acl_add_perm(perms, ACL_WRITE_DATA) == 0);
    REQUIRE(acl_set_file(parent, ACL_TYPE_EXTENDED, parent_acl) == 0);
    note(out,n,"parent: everyone allow read/write, file_inherit; acl_set_file=0\n");

    // Empty inode; never write private bytes.
    fd = open(child, O_CREAT|O_EXCL|O_NOFOLLOW|O_RDWR, 0600);
    REQUIRE(fd >= 0);
done:
    if (parent_acl) acl_free(parent_acl);
    return fd;
}

static int darwin_acl_probe(int fd, const char *child, char *out, size_t n) {
    acl_t before = NULL, empty = NULL, after = NULL;
    acl_entry_t entry;
    acl_permset_t perms;
    acl_flagset_t flags;
    uuid_t everyone;
    int rc = -1, inherited = 0, count = 0, iter;
    struct stat first, last, path_stat;
    ssize_t textlen;
    char *text = NULL;
    out[0] = 0;
    REQUIRE(uuid_parse("FFFFEEEE-DDDD-CCCC-BBBB-AAAA0000000C", everyone) == 0);
    REQUIRE(fstat(fd, &first) == 0);
    note(out,n,"before: uid=%u mode=%04o dev=%llu ino=%llu size=%lld\n",
         first.st_uid, first.st_mode & 07777, (unsigned long long)first.st_dev,
         (unsigned long long)first.st_ino, (long long)first.st_size);
    REQUIRE(S_ISREG(first.st_mode) && first.st_uid == getuid() &&
            (first.st_mode & 07777) == 0600 && first.st_size == 0 && first.st_nlink == 1);
    REQUIRE(lstat(child, &path_stat) == 0);
    REQUIRE(S_ISREG(path_stat.st_mode) && path_stat.st_dev == first.st_dev &&
            path_stat.st_ino == first.st_ino && path_stat.st_uid == first.st_uid);
    REQUIRE((before = acl_get_fd_np(fd, ACL_TYPE_EXTENDED)) != NULL);
    REQUIRE((text = acl_to_text(before, &textlen)) != NULL);
    note(out,n,"same-fd before ACL (%ld bytes): %.2000s\n", (long)textlen, text);
    acl_free(text); text = NULL;
    iter = ACL_FIRST_ENTRY;
    while (1) {
        int got = acl_get_entry(before, iter, &entry);
        REQUIRE(got >= 0);
        if (got == 0) break;
        iter = ACL_NEXT_ENTRY;
        count++;
        acl_tag_t tag;
        uuid_t *who;
        int read_data, write_data, inherited_flag;
        REQUIRE(acl_get_tag_type(entry, &tag) == 0);
        who = acl_get_qualifier(entry);
        REQUIRE(who != NULL);
        REQUIRE(acl_get_permset(entry, &perms) == 0);
        read_data = acl_get_perm_np(perms, ACL_READ_DATA);
        write_data = acl_get_perm_np(perms, ACL_WRITE_DATA);
        REQUIRE(acl_get_flagset_np(entry, &flags) == 0);
        inherited_flag = acl_get_flag_np(flags, ACL_ENTRY_INHERITED);
        REQUIRE(read_data >= 0 && write_data >= 0 && inherited_flag >= 0);
        if (tag == ACL_EXTENDED_ALLOW && uuid_compare(*who, everyone) == 0 &&
            inherited_flag == 1 && read_data == 1 && write_data == 1) inherited++;
        acl_free(who);
    }
    note(out,n,"same-fd before entries=%d inherited everyone read/write=%d\n",count,inherited);
    REQUIRE(inherited > 0);

    REQUIRE((empty = acl_init(0)) != NULL);
    errno = 0;
    int set_result = acl_set_fd_np(fd, empty, ACL_TYPE_EXTENDED);
    int set_errno = errno;
    note(out,n,"acl_set_fd_np(empty)=%d errno=%d (%s)\n",set_result,set_errno,strerror(set_errno));
    REQUIRE(fstat(fd, &last) == 0);
    REQUIRE(lstat(child, &path_stat) == 0);
    note(out,n,"after: uid=%u mode=%04o dev=%llu ino=%llu size=%lld; path dev=%llu ino=%llu\n",
         last.st_uid,last.st_mode & 07777,(unsigned long long)last.st_dev,
         (unsigned long long)last.st_ino,(long long)last.st_size,
         (unsigned long long)path_stat.st_dev,(unsigned long long)path_stat.st_ino);
    errno = set_errno;
    REQUIRE(set_result == 0);
    errno = 0;
    after = acl_get_fd_np(fd, ACL_TYPE_EXTENDED);
    int get_errno = errno;
    note(out,n,"same-fd acl_get_fd_np(after)=%s errno=%d (%s)\n",
         after == NULL ? "NULL" : "non-NULL",get_errno,strerror(get_errno));
    errno = get_errno;
    REQUIRE(after != NULL); // ENOENT is inconclusive, never a clean ACL.
    REQUIRE((text = acl_to_text(after, &textlen)) != NULL);
    note(out,n,"same-fd after ACL (%ld bytes): %.2000s\n",(long)textlen,text);
    acl_free(text); text = NULL;
    iter = ACL_FIRST_ENTRY;
    count = 0;
    while (1) {
        int got = acl_get_entry(after, iter, &entry);
        REQUIRE(got >= 0);
        if (got == 0) break;
        count++;
        iter = ACL_NEXT_ENTRY;
    }
    note(out,n,"same-fd after entries=%d\n",count);
    REQUIRE(count == 0);
    REQUIRE(S_ISREG(last.st_mode) && last.st_uid == first.st_uid &&
            (last.st_mode & 07777) == 0600 && last.st_size == 0 && last.st_nlink == 1 &&
            last.st_dev == first.st_dev && last.st_ino == first.st_ino &&
            S_ISREG(path_stat.st_mode) && path_stat.st_dev == last.st_dev &&
            path_stat.st_ino == last.st_ino && path_stat.st_uid == last.st_uid &&
            (path_stat.st_mode & 07777) == 0600);
    rc = 0;
done:
    if (text) acl_free(text);
    if (after) acl_free(after);
    if (empty) acl_free(empty);
    if (before) acl_free(before);
    return rc;
#undef REQUIRE
}
*/
import "C"

import "unsafe"

func darwinACLPrepare(parent, child string) (int, string) {
	p, c := C.CString(parent), C.CString(child)
	defer C.free(unsafe.Pointer(p))
	defer C.free(unsafe.Pointer(c))
	var out [8192]C.char
	fd := C.darwin_acl_prepare(p, c, &out[0], C.size_t(len(out)))
	return int(fd), C.GoString(&out[0])
}

func darwinACLProbe(fd int, child string) (string, bool) {
	c := C.CString(child)
	defer C.free(unsafe.Pointer(c))
	var out [8192]C.char
	ok := C.darwin_acl_probe(C.int(fd), c, &out[0], C.size_t(len(out))) == 0
	return C.GoString(&out[0]), ok
}

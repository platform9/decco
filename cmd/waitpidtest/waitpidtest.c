#include <stdio.h>
#include <stdarg.h>
#include <errno.h>
#include <unistd.h>
#include <sys/wait.h>

int main( void ) {
    int pid = fork();

    if ( pid == 0 ) {
        printf( "This is being printed from the child process\n" );
        int ret;
        ret = execl("/bin/bash", "bash", "-c", "while true; do echo hello; sleep 3; done", NULL);
        printf( "Sorry, execl failed, errno: %d\n", errno);
    } else {
        printf( "This is being printed in the parent process:\n"
                " - the process identifier (pid) of the child is %d\n", pid );
        int status;
        int child_pid;

        child_pid = waitpid(pid, &status, 0);
        printf("waitpid return value: %d\n", child_pid);
        printf("child exit status: %d\n", status);
    }
    return 0;
}

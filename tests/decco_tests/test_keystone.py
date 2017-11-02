import logging
LOG = logging.getLogger(__name__)

from integration.test_util import BaseTestCase
from decco_tests.testbeds.decco import DeccoTestbed
from proboscis import test, before_class
from pf9lab.testbeds.loader2 import load_testbed
from pf9lab.du.auth import login
import os

@test(groups=['integration'])
class TestKeystone(BaseTestCase):
    @before_class
    def setUp(self):
        LOG.info('In decco keystone test setUp')
        testbed_file = os.getenv('TESTBED')
        self.assertTrue(testbed_file is not None)
        self._tb = load_testbed(testbed_file)
        self.assertTrue(isinstance(self._tb, DeccoTestbed))
        self.URL = 'https://%s' % self._tb.du_fqdn()
        self.USERNAME = self._tb.du_user()
        self.PASSWD = self._tb.du_pass()
        self.TENANT = 'service'

    # shamelessly yanked from TestLoginDU

    @test
    def test_positive(self):
        auth = login(self.URL, self.USERNAME, self.PASSWD, self.TENANT)
        LOG.info('positive login: response = %s', auth)

    @test
    def test_badpassword(self):
        self.assertRaises(Exception, login, self.URL, self.USERNAME,
                          'badpassword', self.TENANT)

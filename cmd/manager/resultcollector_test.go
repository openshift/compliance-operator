package main

import (
	"io/ioutil"
	"os"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Resultcollector", func() {
	Context("Testing result file is waited for", func() {
		var fileName string
		writeResult := func(fName string) {
			err := ioutil.WriteFile(fName, []byte("1"), 0644)
			Expect(err).To(BeNil())
		}
		BeforeEach(func() {
			f, err := ioutil.TempFile("", "result")
			defer f.Close()
			Expect(err).To(BeNil())
			fileName = f.Name()
		})
		AfterEach(func() {
			os.Remove(fileName)
		})
		Context("With the result file pre-existing", func() {
			BeforeEach(func() {
				writeResult(fileName)
			})
			It("immediately reads the file successfully", func() {
				f, err := waitForResultsFile(fileName, 1)
				Expect(err).To(BeNil())
				defer f.Close()
				Expect(f.Name()).To(Equal(fileName))
			})
		})
		Context("With the result file written before the timeout", func() {
			It("reads the file successfully", func() {
				go func() {
					time.Sleep(1 * time.Second)
					writeResult(fileName)
				}()
				f, err := waitForResultsFile(fileName, 3)
				Expect(err).To(BeNil())
				defer f.Close()
				Expect(f.Name()).To(Equal(fileName))
			})
		})
	})

	Context("Testing result file timeout", func() {
		It("panics if the result file isn't there before the timeout", func() {
			f, err := waitForResultsFile("result", 1)
			Expect(f).To(BeNil())
			Expect(err).ToNot(BeNil())
			Expect(err).To(BeEquivalentTo(timeoutErr))
		})
	})
})
